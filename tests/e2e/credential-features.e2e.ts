// Positive upstream-account cases require credentials for disposable fixtures.
// They never borrow a saved personal token or mutate an existing connection.
import assert from 'node:assert/strict';
import {test} from '@e2e-dev/web';
import {dsmPage} from '@synopackageland/cli/e2e';
import {connections, data, go, openConnect, root, waitState, writes, readOnlyReason} from './support/connect.ts';
import {until} from './support/ctl.ts';
import {publicTraffic, servesFixture} from './support/public-traffic.ts';

const options = {session: 'dsm', timeout: 240_000, skip: !writes && readOnlyReason};

async function protect(name: string, value: string): Promise<void> {
  const {processSecrets} = await import(new URL('run/secrecy.js', import.meta.resolve('e2e')).href);
  processSecrets.register(name, value);
}

for (const provider of ['tailscale', 'netbird']) {
  test(`${provider} fixture key connects a real private network and stops without claiming external verification`, options, async ({app}) => {
    const token = process.env[`E2E_${provider.toUpperCase()}_TEST_KEY`];
    test.skip(!token, `requires E2E_${provider.toUpperCase()}_TEST_KEY for a disposable network`);
    await protect(`${provider}.fixture`, token!);
    await openConnect(app);
    test.skip((await connections())[provider]?.state !== 'disconnected', 'requires a disconnected provider');
    try {
      await data('connection.connect', {provider, token});
      await waitState(provider, state => state === 'connected', 120_000);
      const connected = (await connections())[provider];
      assert.equal(connected.networkReady, true);
      const body = await go(provider === 'tailscale' ? 'Tailscale' : 'NetBird', provider);
      await body.locator('[data-tab-panel="apps"]').waitFor({state: 'visible'});
      const check = await until(() => data<{provider: string; state: string; externalVerified: boolean}>('connection.check', {provider}), r => r.state !== 'checking');
      assert.equal(check.provider, provider);
      assert.equal(check.state, 'ok');
      assert.equal(check.externalVerified, false, 'NAS-side checks do not prove another peer can reach it');
    } finally {
      await data('connection.disconnect', {provider});
      await waitState(provider, state => state === 'disconnected');
    }
  });
}

test('GitHub fixture token UI saves only its suffix and removes the credential', options, async ({app}) => {
  const token = process.env.E2E_GITHUB_TEST_TOKEN;
  test.skip(!token, 'requires E2E_GITHUB_TEST_TOKEN for a disposable credential');
  await protect('github.fixture', token!);
  await openConnect(app);
  test.skip((await data<{saved: boolean}>('github.token.status')).saved, 'do not replace an existing credential');
  const body = await go('設定', 'settings');
  await body.locator('[data-ui-tab-value="github-token"]').click();
  try {
    await body.locator('[data-github-token-input] input, input[data-github-token-input]').fill(token!);
    await body.locator('[data-github-token-save]').click();
    const status = await until(() => data<{saved: boolean; last4?: string}>('github.token.status'), s => s.saved);
    assert.equal(status.last4, token!.slice(-4));
    assert.ok(!JSON.stringify(status).includes(token!));
    await body.locator('[data-github-token-remove]').click();
    await until(() => data<{saved: boolean}>('github.token.status'), s => !s.saved);
  } finally {
    await data('github.token.remove');
    assert.equal((await data<{saved: boolean}>('github.token.status')).saved, false);
  }
});

test('disposable custom App publishes, pauses, resumes and removes a real test origin', {...options, timeout: 1_800_000}, async ({app}) => {
  const url = process.env.E2E_CONNECT_TEST_ORIGIN;
  const scope = process.env.E2E_CONNECT_TEST_SCOPE;
  test.skip(!url || !['public', 'relay'].includes(scope ?? ''), 'requires a test-created HTTP origin and E2E_CONNECT_TEST_SCOPE=public|relay on a disposable configured provider');
  await protect('custom.origin', url!);
  await openConnect(app);
  const name = `test-connect-app-${Date.now()}`;
  const hostname = process.env.E2E_CONNECT_TEST_HOSTNAME;
  let id: string | undefined;
  type Row = {package: string; name: string; public?: {state: string; url: string}; relay?: {state: string; url: string}};
  const list = () => data<{apps: Row[]}>('apps.list');
  const method = scope === 'relay' ? 'apps.setrelay' : 'apps.setpublic';
  const mutationResult = (reply: {results: {package: string; ok: boolean; code?: string}[]}) => {
    const result = reply.results.find(result => result.package === id);
    assert.equal(result?.ok, true, result?.code ?? 'publication did not return a result for the fixture');
  };
  try {
    id = (await data<{id: string}>('apps.customadd', {name, url, appSignIn: true, acceptRisk: true, insecureTls: false})).id;
    const inspect = () => data<{ready: boolean; port: number; pin: unknown; token: string; hostname?: string}>('apps.inspect', {package: id, scope, ...(hostname ? {hostname} : {})});
    const inspected = await inspect();
    assert.equal(inspected.ready, true, 'test origin must be running');
    const approval = {port: inspected.port, pin: inspected.pin, token: inspected.token, hostname: inspected.hostname};
    mutationResult(await data(method, {packages: [id], enabled: true, approvals: {[id!]: approval}}));
    const row = await until(async () => (await list()).apps.find(r => r.package === id), r => r?.[scope as 'public' | 'relay']?.state === 'active');
    const address = row![scope as 'public' | 'relay']!.url;
    await protect('custom.address', address);
    const traffic = publicTraffic(address, process.env.E2E_CONNECT_TRAFFIC_EVIDENCE || '.e2e/public-traffic',
      await dsmPage().evaluate(() => navigator.userAgent));
    assert.equal(await traffic.waitUntilServed('published'), true, 'remote traffic returns the fixture body');
    await data('apps.custompause', {id});
    assert.equal((await list()).apps.find(r => r.package === id)?.[scope as 'public' | 'relay']?.state, 'paused');
    const paused = await traffic.request('paused');
    assert.equal(paused.completed, true, 'pause denial must be an HTTP response, not a DNS or connection failure');
    assert.equal(servesFixture(paused), false, 'pausing prevents the fixture from being served');
    const fresh = await inspect();
    mutationResult(await data('apps.reenable', {package: id, scope, approval: {port: fresh.port, pin: fresh.pin, token: fresh.token, hostname: fresh.hostname}}));
    assert.equal(await traffic.waitUntilServed('resumed'), true, 'resuming serves the same fixture');
    await go('App', 'apps');
    await root().locator(`[data-app-row="${id}"]`).waitFor({state: 'visible'});
    await data('apps.customremove', {id});
    const removed = await traffic.request('removed');
    assert.equal(removed.completed, true, 'removal denial must be an HTTP response, not a DNS or connection failure');
    assert.equal(servesFixture(removed), false, 'removing the fixture removes its published route');
  } finally {
    for (const row of (await list()).apps.filter(r => r.package === id || r.name === name))
      await data('apps.customremove', {id: row.package});
  }
});
