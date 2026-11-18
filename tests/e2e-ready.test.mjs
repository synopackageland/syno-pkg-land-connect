import test from 'node:test';
import assert from 'node:assert/strict';
import {readyDesktop} from './e2e/support/connect.ts';

test('a slow restored DSM desktop is reopened until both desktop and signed-in tray are ready', async () => {
  let desktopReads = 0;
  const opened = [];
  const waited = [];
  const page = {locator: selector => ({
    isVisible: async () => false,
    waitFor: async () => {
      waited.push(selector);
      if (selector === '#sds-desktop' && ++desktopReads < 3) {
        const error = new Error('desktop is still restoring');
        error.name = 'TimeoutError';
        throw error;
      }
    },
  })};
  await readyDesktop({open: async () => {}}, () => page, async (_app, path) => { opened.push(path); });
  assert.deepEqual(opened, ['/', '/', '/']);
  assert.equal(waited.at(-1), '#sds-taskbar-tray-item-user');
});

test('a confirmed non-timeout failure is surfaced instead of reopening forever', async () => {
  const denied = new Error('navigation refused');
  const page = {locator: () => ({waitFor: async () => { throw denied; }})};
  await assert.rejects(readyDesktop({open: async () => {}}, () => page, async () => {}), error => error === denied);
});
