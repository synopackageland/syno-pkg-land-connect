// Permission boundary: Connect is admin-only (entry: admin-only). An ordinary
// DSM user never gets the app, and every backend route refuses them.
// Each test signs normal1 in itself: other runs on the test NAS change normal1's
// app privileges, which ends its DSM sessions mid-run.
import {test} from '@e2e-dev/web';
import {expect} from 'e2e';
import assert from 'node:assert/strict';
import {openDesktop, signIn, waitForDesktop} from '@synopackageland/cli/e2e';
import {APP_ID, call, root} from './support/connect.ts';

test('an ordinary user is refused by every Connect route', {session: 'dsm-normal1', timeout: 120_000}, async ({app}) => {
  await signIn(app, 'normal1');
  await openDesktop(app);
  const status = await call('app.status');
  expect(status.success).toBe(false);
  expect(status.error?.code).toBe('admin_required');
  for (const method of ['connection.status', 'binaries.status', 'connection.connect', 'connection.disconnect', 'binaries.check']) {
    const reply = await call(method, {provider: 'netbird', product: 'netbird'});
    assert.equal(reply.error?.code, 'admin_required', method);
  }
});

test('an ordinary user launching Connect sees no Connect pages', {session: 'dsm-normal1', timeout: 120_000}, async ({agent, app}) => {
  await signIn(app, 'normal1');
  const p = await openDesktop(app);
  await app.open(`/?launchApp=${encodeURIComponent(APP_ID)}`);
  await waitForDesktop(p);
  await p.waitForTimeout(5_000);
  assert.equal(await root(p).locator('[data-page]').count(), 0);
  // A refusal reads as missing permission, never as "usually temporary".
  const shown = await root(p).allInnerTexts();
  console.log('normal1 sees:', JSON.stringify(shown.join(' ').slice(0, 200)));
  assert.ok(!shown.join(' ').includes('通常是暫時'), 'the refusal does not call itself temporary');
  await agent.assert('No "Syno Connect Center" / "Syno Connect Center" window shows connection settings; either no such window is open or it only shows a refusal (for example that administrators only may use it).');
});
