import test from 'node:test';
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {mkdtempSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {requestPinned, servesFixture} from './e2e/support/public-traffic.ts';

// Exercise real curl against a local server. This name has no corporate/public
// DNS entry: only --resolve can reach it, and its HTTP Host must stay unchanged.
test('pinned traffic preserves Host and verifies publish/pause/resume/remove responses', async () => {
  let state = 'published';
  const hosts = [];
  const server = createServer((request, response) => {
    hosts.push(request.headers.host);
    const live = state === 'published' || state === 'resumed';
    response.writeHead(live ? 200 : state === 'paused' ? 403 : 404);
    response.end(live ? 'connect-coverage-fixture\n' : 'route unavailable');
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const port = server.address().port;
  const directory = mkdtempSync(join(tmpdir(), 'connect-public-traffic-'));
  try {
    for (state of ['published', 'paused', 'resumed', 'removed']) {
      const reply = await requestPinned(`http://connect-fixture.invalid:${port}/`, '127.0.0.1', directory, state);
      assert.equal(reply.completed, true, state);
      assert.equal(servesFixture(reply), ['published', 'resumed'].includes(state), state);
    }
    assert.deepEqual(hosts, Array(4).fill(`connect-fixture.invalid:${port}`));
  } finally { server.close(); rmSync(directory, {recursive: true, force: true}); }
});

test('an unreachable pinned origin cannot count as an HTTP pause/removal denial', async () => {
  const server = createServer();
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const port = server.address().port;
  await new Promise(resolve => server.close(resolve));
  const directory = mkdtempSync(join(tmpdir(), 'connect-public-traffic-'));
  try {
    const reply = await requestPinned(`http://connect-fixture.invalid:${port}/`, '127.0.0.1', directory, 'unreachable');
    assert.equal(reply.completed, false);
    assert.equal(reply.status, 0);
    assert.equal(servesFixture(reply), false);
  } finally { rmSync(directory, {recursive: true, force: true}); }
});
