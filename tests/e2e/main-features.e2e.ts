// Outcome checks use the real DSM window and App handlers. No route interception.
import assert from 'node:assert/strict';
import {test} from '@e2e-dev/web';
import {dsmPage} from '@synopackageland/cli/e2e';
import {call, clipboard, connections, data, dialog, go, grantClipboard, openConnect, root,
  readOnlyReason, waitState, writes} from './support/connect.ts';
import {until} from './support/ctl.ts';

type AppRow = {package: string; name: string; custom?: {url: string; appSignIn: boolean; reachable?: boolean}; public?: unknown; relay?: unknown; private?: unknown};
type Apps = {apps: AppRow[]};
const options = {session: 'dsm', timeout: 240_000};
const mutation = {...options, skip: !writes && readOnlyReason};
const rows = () => data<Apps>('apps.list');

test('setup wizard cancellation keeps every saved connection unchanged', options, async ({agent, app}) => {
  await openConnect(app);
  const before = await connections();
  await agent.act('In Syno Connect Center Overview, open the setup guide to add a connection method. Stop at the method choices; do not connect or change a setting.');
  const wizard = dialog('[data-setup-wizard]');
  await wizard.waitFor({state: 'visible'});
  assert.equal(await wizard.locator('[data-wizard-way="tailscale"]').count(), 1);
  // WizardDialog forwards the marker to its body; its header close button
  // belongs to the containing Dialog. Focus a choice so local Escape routes
  // through this App's portal layer and cancels the actual wizard.
  await wizard.locator('[data-wizard-way="tailscale"]').focus();
  await dsmPage().keyboard.press('Escape');
  await wizard.waitFor({state: 'hidden'});
  assert.deepEqual(await connections(), before);
});

test('custom App handlers create, edit and remove only a new fixture without publishing it', mutation, async ({app}) => {
  await openConnect(app);
  const name = `Coverage ${Date.now()}`;
  const before = (await rows()).apps.map(r => r.package).sort();
  let id: string | undefined;
  try {
    id = (await data<{id: string}>('apps.customadd', {name, url: 'http://127.0.0.1:48991', appSignIn: false, insecureTls: false})).id;
    assert.ok(id);
    let created = (await rows()).apps.find(r => r.package === id);
    assert.equal(created?.name, name);
    assert.equal(created?.custom?.appSignIn, false);
    assert.ok(!created?.public && !created?.relay && !created?.private, 'saving a target does not publish it');
    await data('apps.customedit', {id, name: `${name} edited`, url: 'http://127.0.0.1:48992', appSignIn: false, insecureTls: false});
    created = (await rows()).apps.find(r => r.package === id);
    assert.equal(created?.name, `${name} edited`);
    assert.equal(created?.custom?.url, 'http://127.0.0.1:48992');
    await data('apps.customremove', {id});
    assert.ok(!(await rows()).apps.some(r => r.package === id));
  } finally {
    // Reconcile a lost add reply by the unique name, never delete another App.
    for (const row of (await rows()).apps.filter(r => r.package === id || r.name === name || r.name === `${name} edited`))
      await data('apps.customremove', {id: row.package});
    assert.deepEqual((await rows()).apps.map(r => r.package).sort(), before);
  }
});

test('Apps UI adds, edits and removes a custom target with persisted readback', mutation, async ({app}) => {
  await openConnect(app);
  await go('App', 'apps');
  // This header action is registered only after apps.list settles and a real
  // App-capable way is available. Absence during loading is not a skip.
  await root().locator('[data-apps-add]').waitFor({state: 'visible', timeout: 0});
  const name = `Coverage UI ${Date.now()}`;
  let id: string | undefined;
  try {
    await root().locator('[data-apps-add]').click();
    const form = dialog('[data-custom-dialog]');
    const origin = process.env.E2E_CONNECT_TEST_ORIGIN || 'http://127.0.0.1:48993';
    await form.locator('[data-custom-url] input').fill(origin);
    await form.locator('[data-custom-name] input').fill(name);
    await form.locator('[data-custom-save]').click();
    const created = await until(async () => (await rows()).apps.find(r => r.name === name), r => !!r);
    id = created!.package;
    assert.equal(created!.custom?.url, origin);
    if (process.env.E2E_CONNECT_TEST_ORIGIN) assert.equal(created!.custom?.reachable, true, 'the test-created origin is reachable');
    await root().locator(`[data-app-open="${id}"]`).click();
    await dialog('[data-app-dialog]').locator('[data-dialog-custom-edit]').click();
    await form.locator('[data-custom-name] input').fill(`${name} edited`);
    await form.locator('[data-custom-save]').click();
    await until(async () => (await rows()).apps.find(r => r.package === id)?.name, n => n === `${name} edited`);
    await root().locator(`[data-app-open="${id}"]`).click();
    await dialog('[data-app-dialog]').locator('[data-dialog-custom-edit]').click();
    await form.locator('[data-custom-remove-start]').click();
    await form.locator('[data-custom-remove-confirm]').click();
    await until(rows, v => !v.apps.some(r => r.package === id));
    await root().locator(`[data-app-row="${id}"]`).waitFor({state: 'detached'});
  } finally {
    for (const row of (await rows()).apps.filter(r => r.package === id || r.name === name || r.name === `${name} edited`))
      await data('apps.customremove', {id: row.package});
  }
});

test('custom target risk refusal preserves the saved target and its sign-in gate', mutation, async ({app}) => {
  await openConnect(app);
  const name = `Coverage risk ${Date.now()}`;
  let id: string | undefined;
  try {
    id = (await data<{id: string}>('apps.customadd', {name, url: 'http://127.0.0.1:48994', appSignIn: false, insecureTls: false})).id;
    const before = (await rows()).apps.find(r => r.package === id);
    const refused = await call('apps.customedit', {id, name, url: 'http://127.0.0.1:48994', appSignIn: true, insecureTls: false});
    assert.equal(refused.success, false, 'removing the DSM gate needs explicit risk confirmation');
    assert.ok(refused.error?.code);
    assert.deepEqual((await rows()).apps.find(r => r.package === id), before);
  } finally {
    for (const row of (await rows()).apps.filter(r => r.package === id || r.name === name))
      await data('apps.customremove', {id: row.package});
  }
});

test('IPv6 refresh reads real prerequisites without changing provider configuration', options, async ({app}) => {
  await openConnect(app);
  const before = (await connections()).ipv6;
  const body = await go('IPv6', 'ipv6');
  const observed = dsmPage().waitForResponse(async response => {
    const request = response.request();
    return request.url().includes('/cgi/api.cgi') && request.postData()?.includes('ipv6.status') === true;
  });
  await body.locator('[data-ipv6-check-again]').click();
  const reply = await (await observed).json();
  assert.equal(reply.success, true);
  assert.ok(reply.data && typeof reply.data === 'object');
  assert.deepEqual((await connections()).ipv6, before);
});

test('GitHub token UI rejects a malformed fixture without saving it', mutation, async ({app}) => {
  await openConnect(app);
  const before = await data<{saved: boolean}>('github.token.status');
  test.skip(before.saved, 'do not replace an existing credential');
  const body = await go('設定', 'settings');
  await body.locator('[data-ui-tab-value="github-token"]').click();
  await body.locator('[data-github-token-input] input, input[data-github-token-input]').fill('coverage-invalid');
  const observed = dsmPage().waitForResponse(r => r.request().postData()?.includes('github.token.set') === true);
  await body.locator('[data-github-token-save]').click();
  const reply = await (await observed).json();
  assert.equal(reply.success, false);
  assert.equal(reply.error?.code, 'github_token_malformed');
  assert.deepEqual(await data('github.token.status'), before);
  await body.locator('[data-github-token-cancel]').click();
});

test('NetBird self-hosted server missing prerequisites refuse setup without starting a server', mutation, async ({app}) => {
  await openConnect(app);
  const before = await data<{state: string; missing?: string[]}>('netbirdserver.status');
  test.skip(before.state !== 'off', 'requires no existing self-hosted server');
  const cloudflare = (await connections()).cloudflare;
  test.skip(cloudflare?.state === 'connected', 'negative fixture requires no Cloudflare domain');
  try {
    const reply = await call<{state: string; code?: string}>('netbirdserver.setup');
    const current = await until(() => data<{state: string; code?: string; missing?: string[]; job?: {state?: string}}>('netbirdserver.status'), s => !['working', 'starting'].includes(s.state));
    assert.notEqual(current.state, 'running');
    assert.ok(!reply.success || current.code || current.missing?.length, 'refusal identifies the missing prerequisite');
    const body = await go('NetBird', 'netbird');
    await body.locator('[data-ui-tab-value="server"]').click();
    await body.locator('[data-nbserver-prereq], [data-nbserver-failure], [data-nbserver-unavailable]').first().waitFor({state: 'visible'});
  } finally {
    await data('netbirdserver.disable', {removeData: true});
    assert.equal((await data<{state: string}>('netbirdserver.status')).state, 'off');
  }
});

test('temporary Cloudflare UI starts a reachable address, copies it, checks and stops it', {...mutation, timeout: 600_000}, async ({app}) => {
  test.skip(process.env.E2E_CONNECT_REPLACE_CLOUDFLARE !== '1', 'requires explicit temporary replacement and restoration of Cloudflare');
  await openConnect(app);
  const saved = (await connections()).cloudflare;
  assert.ok(saved?.state === 'disconnected' || saved?.state === 'connected' && saved.mode === 'domain', 'only a stopped provider or restorable own domain may be replaced');
  const body = await go('Cloudflare', 'cloudflare');
  try {
    if (saved.state === 'connected') {
      await data('connection.disconnect', {provider: 'cloudflare'});
      await waitState('cloudflare', s => s === 'disconnected');
    }
    await body.locator('[data-ui-tab-value="temporary"]').click();
    await body.locator('[data-cf-quick]').click();
    await waitState('cloudflare', s => s === 'connected', 120_000);
    const status = (await connections()).cloudflare;
    const address = status?.endpoints?.[0]?.url;
    assert.ok(address);
    // A name/status alone does not prove that traffic reaches the NAS.
    const reachable = await until(async () => {
      try {
        const response = await dsmPage().request.get(address!, {timeout: 10_000});
        return response.status() === 200 && /Synology|SYNO|webman|login/i.test(await response.text());
      } catch { return false; }
    }, ok => ok, 120_000);
    assert.equal(reachable, true);
    await body.locator('[data-ui-tab-value="connection"]').click();
    await grantClipboard();
    await body.locator('[data-url-card]').first().getByRole('button', {name: '複製連結'}).click();
    assert.equal(await clipboard(), address);
    const check = await until(() => data<{state: string; externalVerified: boolean}>('connection.check', {provider: 'cloudflare'}), r => !['checking', 'starting'].includes(r.state), 120_000);
    assert.equal(check.state, 'ok');
    await body.locator('[data-ui-tab-value="temporary"]').click();
    await body.locator('[data-cf-temporary-stop]').click();
    await dialog('[data-cloudflare-off-dialog]').locator('[data-cloudflare-off-confirm]').click();
    await waitState('cloudflare', s => s === 'disconnected');
  } finally {
    if ((await connections()).cloudflare?.state !== 'disconnected') {
      await data('connection.disconnect', {provider: 'cloudflare'});
      await waitState('cloudflare', s => s === 'disconnected');
    }
    if (saved.state === 'connected') {
      await data('connection.connect', {provider: 'cloudflare', mode: 'domain', confirmExposure: true});
      await waitState('cloudflare', s => s === 'connected', 120_000);
      const restored = (await connections()).cloudflare;
      assert.equal(restored.mode, saved.mode);
      assert.equal(restored.hostname, saved.hostname, 'the exact previous own-domain connection is restored');
    }
  }
});
