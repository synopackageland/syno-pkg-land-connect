// NetBird sign-in: waiting with a countdown, the user code and its copy
// button, the timeout, and copying the NetBird IP once connected.
import {describe, test} from '@e2e-dev/web';
import assert from 'node:assert/strict';
import {dismissNotifications, dsmPage, dsmUrl, openDesktop, waitForDesktop} from '@synopackageland/cli/e2e';
import {call, clipboard, connections, go, grantClipboard, netbirdStuck, openConnect, packageRunning, readOnlyReason, saveDaemonLog,
  startPackage, stopPackage, waitState, withConnectedNetBird, writes} from './support/connect.ts';

const seconds = (text: string) => text.match(/(\d+)/g)!.map(Number).reduce((total, part) => total * 60 + part, 0);

// A failed token poll during sign-in was
// reported as login_output_invalid. It must now be a real failure code, and
// 「登入 NetBird」 starts a new attempt back at needs-login; 重新檢查 only reads.
// Declared first: the sign-in tests below stop NetBird.
describe('regression C1: NetBird after a timed-out sign-in and a package restart', {serial: true, session: 'dsm', timeout: 1_200_000,
  skip: !writes && readOnlyReason}, () => {
  test('signing in again after the failure returns to needs-login', async ({app}) => {
    await openConnect(app);
    const before = (await connections()).netbird;
    console.log('netbird before:', JSON.stringify({state: before?.state, code: before?.code}));
    if (before?.state !== 'failed') {
      test.skip(process.env.E2E_CONNECT_SLOW !== '1', 'reproducing C1 waits out a 10-minute NetBird sign-in; set E2E_CONNECT_SLOW=1');
      const started = await call('connection.connect', {provider: 'netbird'});
      assert.ok(started.success, `starting the NetBird sign-in: ${started.error?.code}`);
      await waitState('netbird', s => s === 'failed' || s === 'disconnected', 900_000);
      const ended = (await connections()).netbird;
      console.log('netbird after the wait:', JSON.stringify({state: ended?.state, code: ended?.code}));
    }
    await stopPackage();
    try { await startPackage(); } finally { if (!await packageRunning()) await startPackage(); }
    await dsmPage().reload();
    await openConnect(app);
    const body = await go('NetBird', 'netbird');
    const restarted = (await connections()).netbird;
    console.log('netbird after restart:', JSON.stringify({state: restarted?.state, code: restarted?.code}));
    try {
      if (netbirdStuck(restarted)) console.log('daemon log:', await saveDaemonLog('c1-netbird-daemon.log'));
      assert.ok(!netbirdStuck(restarted), 'a failed sign-in is not reported as login_output_invalid');
      if (restarted?.state === 'failed') {
        await body.getByRole('button', {name: '登入 NetBird'}).click();
        await waitState('netbird', s => s === 'needs-login', 120_000);
      }
      assert.equal((await connections()).netbird?.state, 'needs-login', 'NetBird waits for a new sign-in');
      await body.locator('[data-nb-countdown]').waitFor({state: 'visible', timeout: 30_000});
    } finally {
      await stop(body);
    }
  });
});

// The page served over plain http from a non-loopback host is not
// a secure context, so it has no Clipboard API. The browser reaches DSM as
// http://nas.example/ through the tunnel, and connection.status is rewritten
// to a connected NetBird, so no setup key or state change is needed.
describe('regression C3: copying the NetBird IP over http', {serial: true, session: 'dsm', timeout: 180_000}, () => {
  test('the copy IP button says it copied', async ({app}) => {
    const ip = '100.92.10.7';
    const insecure = 'http://nas.example/';
    const p = await openDesktop(app);
    await p.context().route(`${insecure}**`, async route => {
      const request = route.request();
      // DSM checks the request's Host against its Origin, so the Host stays the http one.
      const response = await route.fetch({url: dsmUrl() + request.url().slice(insecure.length), maxRedirects: 0,
        headers: {...request.headers(), host: new URL(insecure).host}});
      const status = request.url().includes('/SynoPkgConnect/cgi/api.cgi') && JSON.parse(request.postData() || '{}').method === 'connection.status';
      await route.fulfill(status ? {response, json: withConnectedNetBird(await response.json(), ip)} : {response});
    });
    // route.fetch sends the tunnel origin's cookies, so the signed-in session carries over.
    await p.goto(insecure);
    await waitForDesktop(p);
    await dismissNotifications(p);
    assert.equal(await p.evaluate(() => window.isSecureContext), false, 'http on a non-loopback host is not a secure context');
    await openConnect(app);
    const copy = (await go('NetBird', 'netbird')).getByRole('button', {name: '複製 IP 位址'});
    await copy.waitFor({state: 'visible', timeout: 20_000});
    const button = (await copy.elementHandle())!;
    await copy.click();
    // The copied state lasts 1.5 s, so it is read right after the click.
    const shown = await p.waitForFunction(el => el.getAttribute('data-copy-state') && el, button, {timeout: 3_000})
      .then(() => button.evaluate(el => `${el.getAttribute('data-copy-state')} ${el.getAttribute('aria-label')}`), () => 'no copy state');
    await p.context().unrouteAll({behavior: 'ignoreErrors'});
    console.log('copy IP button:', shown);
    // Asserted after the click: a hover alone never says 已複製.
    assert.equal(shown, 'copied 已複製');
  });
});

describe('NetBird status', {serial: true, session: 'dsm', timeout: 120_000}, () => {
  test('a failed sign-in shows the backend code once', async ({app}) => {
    await openConnect(app);
    const body = await go('NetBird', 'netbird');
    const status = (await connections()).netbird;
    // The badge starts as disabled and follows connection.status once it answers.
    await body.locator(`[data-connection-badge][data-connection-state="${status?.state ?? 'disconnected'}"]`).waitFor({state: 'visible', timeout: 20_000});
    if (status?.state !== 'failed' || !status.code) return;
    const shown = await body.locator('[data-nb-error] [data-ui-callout-code], [data-connection-reason]').allInnerTexts();
    assert.equal(shown.length, 1, 'exactly one failure callout');
  });
});

describe('NetBird sign-in', {serial: true, session: 'dsm', timeout: 900_000, skip: !writes && readOnlyReason}, () => {
  test('SSO sign-in waits with a countdown and a copyable user code', async ({agent, app}) => {
    await openConnect(app);
    await grantClipboard();
    const body = await go('NetBird', 'netbird');
    try {
      // The user code can take longer than the agent waits; the countdown wait below covers it.
      await agent.act('On the NetBird page of Syno Connect Center, start signing in with SSO (no setup key), then stop; do not open the sign-in page.');
      await body.locator('[data-nb-countdown]').waitFor({state: 'visible', timeout: 120_000});
      const status = (await connections()).netbird!;
      assert.ok(status.userCode, 'the backend issued a user code');
      const first = seconds(await body.locator('[data-nb-countdown]').innerText());
      assert.ok(first > 0 && first <= 600, `countdown ${first}s is within 10 minutes`);
      await dsmPage().waitForTimeout(3_000);
      const during = (await connections()).netbird;
      console.log('netbird during sign-in:', JSON.stringify({state: during?.state, code: during?.code, phase: during?.phase}));
      assert.ok(seconds(await body.locator('[data-nb-countdown]').innerText()) < first, 'the countdown runs');
      await body.getByRole('button', {name: '複製代碼'}).click();
      assert.equal(await clipboard(), status.userCode);
      if (process.env.E2E_CONNECT_SLOW === '1') {
        // The sign-in expires after its deadline; the page must say so.
        await waitState('netbird', s => s === 'failed' || s === 'disconnected', 660_000);
        await body.locator('[data-nb-error], [data-connection-reason]').first().waitFor({state: 'visible'});
        assert.equal(await body.locator('[data-nb-countdown]').count(), 0, 'the countdown is gone after the timeout');
      }
    } finally {
      await stop(body);
    }
  });
});

describe('NetBird connected', {serial: true, session: 'dsm', timeout: 300_000,
  skip: !writes && readOnlyReason || !process.env.E2E_NETBIRD_SETUP_KEY && 'needs E2E_NETBIRD_SETUP_KEY'}, () => {
  test('a connected NetBird offers its IP to copy', async ({app}) => {
    await openConnect(app);
    await grantClipboard();
    const body = await go('NetBird', 'netbird');
    try {
      await body.locator('[data-advanced-section]').getByRole('button').first().click();
      await body.locator('[data-nb-key]').fill(process.env.E2E_NETBIRD_SETUP_KEY!);
      await body.locator('[data-nb-connect]').click();
      await waitState('netbird', s => s === 'connected', 180_000);
      const ip = (await connections()).netbird!.netbirdIP!;
      await body.getByRole('button', {name: '複製 IP 位址'}).click();
      assert.equal(await clipboard(), ip);
    } finally {
      await stop(body);
    }
  });
});

async function stop(body: import('playwright').Locator): Promise<void> {
  const button = body.locator('[data-stop]');
  if (await button.count()) await button.click();
  await waitState('netbird', s => s === 'disconnected');
}
