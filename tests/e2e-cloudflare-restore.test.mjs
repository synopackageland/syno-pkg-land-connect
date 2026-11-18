// url-card may start a temporary Cloudflare tunnel only when it can put the
// saved Cloudflare setting back afterwards and read it back unchanged.
import test from 'node:test';
import assert from 'node:assert/strict';
import {cloudflareSaved, cloudflareUnrestorable} from './e2e/support/connect.ts';

test('the saved setting is the state, mode and own domain connection.status reports', () => {
  assert.deepEqual(cloudflareSaved({provider: 'cloudflare', state: 'disconnected', mode: 'domain', hostname: 'nas.example.com'}),
    {state: 'disconnected', mode: 'domain', hostname: 'nas.example.com'});
  assert.deepEqual(cloudflareSaved(undefined), {state: 'disconnected', mode: '', hostname: ''});
});

test('a stopped temporary or own-domain tunnel can be restored', () => {
  for (const mode of ['quick', 'domain']) {
    assert.equal(cloudflareUnrestorable({state: 'disconnected', mode, hostname: ''}), false, mode);
  }
});

test('a running tunnel, a token setting or no setting cannot be restored', () => {
  assert.match(cloudflareUnrestorable({state: 'connected', mode: 'domain', hostname: 'nas.example.com'}), /running/);
  assert.match(cloudflareUnrestorable({state: 'disconnected', mode: 'token', hostname: 'nas.example.com'}), /token/);
  // Without settings.json only forgetting Cloudflare would remove the one a temporary tunnel writes.
  assert.match(cloudflareUnrestorable({state: 'disconnected', mode: '', hostname: ''}), /no saved/);
});
