// Update and approval prompts follow the backend: "update to", "takes effect
// on reconnect" and "roll back to" on Settings, and Tailscale's machine
// approval. Connect has no executor, so there is no administrator approval.
import {describe, test} from '@e2e-dev/web';
import assert from 'node:assert/strict';
import {connections, data, go, openConnect, type Binary} from './support/connect.ts';

describe('update and approval prompts', {serial: true, session: 'dsm', timeout: 180_000}, () => {
  test('each connector card offers exactly the update actions its status allows', async ({app}) => {
    await openConnect(app);
    const body = await go('設定', 'settings');
    for (const product of ['cloudflared', 'tailscale', 'netbird']) {
      const card = body.locator(`[aria-label="${product}"]`);
      await card.locator('[data-binary-in-use]').waitFor({state: 'visible', timeout: 30_000});
      const s = await data<Binary>('binaries.status', {product});
      const offered = s.current ? s.updateAvailable && !!s.latest : true;
      const update = card.getByRole('button', {name: s.current ? `更新到 ${s.latest}` : '下載'});
      assert.equal(await update.count(), offered ? 1 : 0, `${product} update button`);
      const pending = !!(s.current && s.runtimeVersion && s.current !== s.runtimeVersion);
      assert.equal(await card.locator('[data-binary-pending]').count(), pending ? 1 : 0, `${product} reconnect notice`);
      assert.equal(await card.locator('[data-binary-rollback]').count(), s.previous ? 1 : 0, `${product} rollback`);
    }
  });

  test('Tailscale asks for machine approval exactly when its tailnet holds it', async () => {
    const body = await go('Tailscale', 'tailscale');
    const status = (await connections()).tailscale;
    const waiting = status?.state === 'needs-machine-auth' || status?.code === 'machine_approval_wait_timeout';
    assert.equal(await body.locator('[data-machine-approval]').count(), waiting ? 1 : 0);
  });
});
