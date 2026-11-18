// url-card and the overview read a connection's addresses from its endpoints.
import test from 'node:test';
import assert from 'node:assert/strict';
import {addressesOf} from './e2e/support/connect.ts';

test('a connection\'s addresses are its endpoint URLs', () => {
  assert.deepEqual(addressesOf({provider: 'cloudflare', state: 'connected',
    endpoints: [{url: 'https://a.trycloudflare.com', public: true, localPort: 5000}]}), ['https://a.trycloudflare.com']);
  assert.deepEqual(addressesOf({provider: 'ipv6', state: 'disconnected'}), []);
});
