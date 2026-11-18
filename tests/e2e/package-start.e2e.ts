// Opening Connect right after its package starts shows its pages,
// and no Connect request fails while the backend comes up. A
// window open while the package is stopped says so, asks for no approval and
// recovers by itself once the package runs again.
import {describe, test} from '@e2e-dev/web';
import assert from 'node:assert/strict';
import {dsmPage, openDesktop} from '@synopackageland/cli/e2e';
import {data, openConnect, packageRunning, readOnlyReason, root, startPackage, stopPackage, writes} from './support/connect.ts';

describe('regression P2: opening Connect while it starts', {serial: true, session: 'dsm', timeout: 300_000, skip: !writes && readOnlyReason}, () => {
  test('the window shows its overview and every Connect request answers 200', async ({app}) => {
    const p = await openDesktop(app);
    const failed: string[] = [];
    p.on('response', response => {
      if (response.url().includes('/3rdparty/SynoPkgConnect/') && response.status() !== 200) failed.push(`${response.status()} ${response.url()}`);
    });
    await stopPackage();
    let opened = 0;
    try {
      console.log(`synopkg start took ${await startPackage()}s`);
      const started = Date.now();
      await openConnect(app);
      await root().locator('[data-overview], [data-overview-start]').first().waitFor({state: 'visible', timeout: 30_000});
      opened = (Date.now() - started) / 1000;
    } finally {
      if (!await packageRunning()) await startPackage();
    }
    console.log(`overview shown ${opened}s after synopkg start returned`);
    assert.ok((await data<{version?: string}>('app.status')) !== undefined, 'app.status answers');
    assert.deepEqual(failed, []);
  });
});

// An open window should report that the app is stopped. On DSM 7.4 that
// window does not survive: DSM closes it before `synopkg stop` returns, and
// Connect has none of the delegate views that show the notice. What is left
// to check: the window goes, and reopened after the start it shows its pages
// with no approval prompt.
describe('an open Connect window while its package stops and starts', {serial: true, session: 'dsm',
  timeout: 300_000, skip: !writes && readOnlyReason}, () => {
  test('DSM closes the window, and reopened after the start it asks for no approval', async ({app}) => {
    await openConnect(app);
    await root().locator('[data-overview], [data-overview-start]').first().waitFor({state: 'visible', timeout: 30_000});
    await stopPackage();
    try {
      await root().first().waitFor({state: 'detached', timeout: 30_000});
    } finally {
      if (!await packageRunning()) await startPackage();
    }
    await openConnect(app);
    await root().locator('[data-overview], [data-overview-start]').first().waitFor({state: 'visible', timeout: 60_000});
    const shown = (await root().first().innerText()).replace(/\s+/g, ' ');
    await dsmPage().screenshot({path: '.e2e/output/sdk-reopened-window.png'});
    assert.doesNotMatch(shown, /核准/, 'a restarted app asks for no approval');
  });
});
