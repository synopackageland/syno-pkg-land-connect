// Tailscale page: Join while off, Apps once connected, and no services or
// Funnel; starting a connection waits for sign-in.
import {describe, test} from '@e2e-dev/web';
import assert from 'node:assert/strict';
import {dsmPage} from '@synopackageland/cli/e2e';
import {connections, go, nasReaches, openConnect, readOnlyReason, waitState, writes} from './support/connect.ts';

describe('Tailscale page', {serial: true, session: 'dsm', timeout: 300_000}, () => {
  test('the page is Join or Apps, without services or Funnel', async ({app}) => {
    await openConnect(app);
    const body = await go('Tailscale', 'tailscale');
    const on = (await connections()).tailscale?.state === 'connected';
    await body.locator(on ? '[data-tab-panel="apps"] [data-apps-page="private"]' : '[data-tab-panel="join"] [data-connect-form="tailscale"]').waitFor({state: 'attached', timeout: 30_000});
    assert.equal(await body.locator('[data-service-id], [data-funnel], [data-port-rows], [data-exposure-confirm]').count(), 0);
  });
});

describe('Tailscale sign-in', {serial: true, session: 'dsm', timeout: 300_000, skip: !writes && readOnlyReason}, () => {
  test('starting without a key waits for sign-in with a countdown and a sign-in link', async ({app}) => {
    test.skip(!await nasReaches('https://controlplane.tailscale.com/key?v=71'), 'test NAS cannot reach controlplane.tailscale.com');
    await openConnect(app);
    assert.equal((await connections()).tailscale?.state ?? 'disconnected', 'disconnected', 'starts from a disabled Tailscale');
    const body = await go('Tailscale', 'tailscale');
    try {
      await body.locator('[data-ts-connect]').click();
      await waitState('tailscale', s => s === 'needs-login', 180_000);
      const status = (await connections()).tailscale!;
      assert.match(status.loginUrl ?? '', /^https:\/\/login\.tailscale\.com\//);
      await body.locator('[data-login-countdown]').waitFor({state: 'visible'});
      const popup = dsmPage().waitForEvent('popup');
      await body.locator('[data-login-action]').first().click();
      const tab = await popup;
      await tab.waitForURL(url => url.protocol !== 'about:', {waitUntil: 'commit', timeout: 30_000});
      assert.equal(tab.url(), status.loginUrl);
      await tab.close();
    } finally {
      await body.locator('[data-stop]').click();
      await waitState('tailscale', s => s === 'disconnected');
    }
  });
});
