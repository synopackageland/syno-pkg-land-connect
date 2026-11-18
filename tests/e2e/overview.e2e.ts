// The Home (總覽) and connection methods: lanes follow the backend's
// connections, Add a way opens the setup wizard dialog; adding a method and
// removing it again.
import {describe, test} from '@e2e-dev/web';
import assert from 'node:assert/strict';
import {dsmPage} from '@synopackageland/cli/e2e';
import {backupConnectFiles, connectFilesMatch, connections, go, openConnect, packageRunning, pageBody,
  readOnlyReason, removeBackup, restoreConnectFiles, root, startPackage, stopPackage, TAILSCALE_FILES, waitState, writes} from './support/connect.ts';
import type {Connection} from './support/connect.ts';

const METHODS = ['cloudflare', 'tailscale', 'netbird', 'ipv6'];

describe('overview and connection methods', {serial: true, session: 'dsm', timeout: 300_000}, () => {
  test('lanes follow the set-up methods, and only a failed one shows red', async ({app}) => {
    await openConnect(app);
    const overview = pageBody('status');
    // [data-overview] shows before its lanes; wait for a lane or, with nothing set up, the first visit.
    await overview.locator('[data-overview] [data-lane-id], [data-home-first]').first().waitFor({state: 'visible', timeout: 30_000});
    const lanes = await overview.locator('[data-overview] [data-lane-id]').evaluateAll(els => els.map(e => e.getAttribute('data-lane-id')));
    if (lanes.length === 0) {
      assert.equal(await overview.locator('[data-home-first]').count(), 1, 'nothing set up shows the first visit');
      return;
    }
    for (const lane of lanes) assert.ok([...METHODS, 'workers'].includes(lane!), `${lane} is a known way`);
    const status = await connections();
    for (const lane of lanes) {
      const failed = status[lane!]?.state === 'failed';
      const errorLinks = await overview.locator(`[data-overview] [data-lane-id="${lane}"] [data-link-state="error"]`).count();
      assert.equal(errorLinks > 0, failed, `${lane} lane shows an error link exactly when it failed`);
    }
  });

  test('Add a way opens the setup wizard dialog', async ({agent}) => {
    const add = pageBody('status').locator('[data-home-add-way]');
    if (!await add.count()) return;
    const label = (await add.innerText()).trim();
    await agent.act(`In the Syno Connect Center window's 總覽 page, use the "${label}" button below the connections.`);
    await root().locator('[data-setup-wizard]').waitFor({state: 'visible', timeout: 10_000});
  });

});

describe('adding and removing a connection method', {serial: true, session: 'dsm', timeout: 300_000, skip: !writes && readOnlyReason}, () => {
  test('adding Tailscale shows its lane and removing it takes the lane away', async ({app}) => {
    await openConnect(app);
    assert.equal((await connections()).tailscale?.state ?? 'disconnected', 'disconnected', 'starts from a disabled Tailscale');
    const backup = await backupConnectFiles(TAILSCALE_FILES);
    try {
      try {
        const body = await go('Tailscale', 'tailscale');
        await body.locator('[data-ts-connect]').click();
        await waitState('tailscale', s => s !== 'disconnected');
        await go('總覽', 'status');
        await root().locator('[data-lane-id="tailscale"]').waitFor({state: 'visible'});
      } finally {
        await go('Tailscale', 'tailscale');
        await pageBody('tailscale').locator('[data-stop]').click();
        await waitState('tailscale', s => s === 'disconnected');
      }
      await go('總覽', 'status');
      await root().locator('[data-lane-id="tailscale"]').waitFor({state: 'detached'});
      await stopPackage();
      try {
        await startPackage();
      } finally {
        if (!await packageRunning()) await startPackage();
      }
      const restarted = await settledConnections();
      assert.equal(restarted.tailscale?.state ?? 'disconnected', 'disconnected', 'a restart keeps Tailscale stopped');
    } finally {
      if (!await connectFilesMatch(backup, TAILSCALE_FILES)) await restoreConnectFiles(backup, TAILSCALE_FILES);
      assert.ok(await connectFilesMatch(backup, TAILSCALE_FILES), `Tailscale files restored from ${backup}`);
      await removeBackup(backup);
    }
  });

  test('IPv6 is a read-only guide and a missing global address is a step, not a failure', async ({app}) => {
    await openConnect(app);
    const saved = (await connections()).ipv6;
    const body = await go('IPv6 直連', 'ipv6');
    await body.locator('[data-ipv6-checklist]').waitFor({state: 'visible'});
    assert.equal(await body.locator('[data-ipv6-connect], [data-ipv6-confirm]').count(), 0);
    assert.equal(await body.locator('[data-connection-reason]').count(), 0);
    await body.locator('[data-ipv6-check-again]').click();
    await body.locator('[data-ipv6-check-again]:not([disabled])').waitFor();
    assert.deepEqual((await connections()).ipv6, saved, 'checking does not enable or change IPv6');
  });
});

/** connection.status once the restarted daemon answers it. */
async function settledConnections(): Promise<Record<string, Connection>> {
  const deadline = Date.now() + 60_000;
  for (;;) {
    try {
      return await connections();
    } catch (error) {
      if (Date.now() > deadline) throw error;
      await dsmPage().waitForTimeout(2_000);
    }
  }
}
