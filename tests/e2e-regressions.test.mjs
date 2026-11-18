// Pure helpers for failed NetBird sign-in and connected-IP regression scenarios.
import test from 'node:test';
import assert from 'node:assert/strict';
import {RATE_LIMITED, githubQuota, staleAlerts, netbirdStuck, redactLog, statusRunning, withConnectedNetBird} from './e2e/support/connect.ts';

test('C1: NetBird is stuck only when failed with login_output_invalid', () => {
  assert.equal(netbirdStuck({provider: 'netbird', state: 'failed', code: 'login_output_invalid'}), true);
  assert.equal(netbirdStuck({provider: 'netbird', state: 'failed', code: 'netbird_timeout'}), false);
  assert.equal(netbirdStuck({provider: 'netbird', state: 'disconnected'}), false);
  assert.equal(netbirdStuck(undefined), false);
});

test('C3: a connection.status reply is rewritten to a connected NetBird with an IP', () => {
  const reply = {success: true, data: {provider: 'cloudflare', state: 'disconnected', connections: [
    {provider: 'cloudflare', state: 'disconnected'}, {provider: 'netbird', state: 'failed', code: 'login_output_invalid'}]}};
  const out = withConnectedNetBird(reply, '100.92.10.7');
  const netbird = out.data.connections.find(c => c.provider === 'netbird');
  assert.deepEqual({state: netbird.state, code: netbird.code, ip: netbird.netbirdIP, ready: netbird.networkReady},
    {state: 'connected', code: undefined, ip: '100.92.10.7', ready: true});
  assert.equal(out.data.connections[0].state, 'disconnected', 'other methods stay as they are');
  assert.equal(reply.data.connections[1].state, 'failed', 'the original reply is not changed');
});

test('C3: a reply without NetBird gains one', () => {
  const out = withConnectedNetBird({success: true, data: {connections: []}}, '100.92.10.7');
  assert.equal(out.data.connections[0].provider, 'netbird');
});

test('C4: GitHub\'s core quota is used up when remaining is 0, and says until when', () => {
  assert.deepEqual(githubQuota('{"resources":{"core":{"limit":60,"remaining":0,"reset":1791993785}}}'),
    {exhausted: true, resetAt: new Date(1791993785 * 1000).toISOString()});
  assert.equal(githubQuota('{"resources":{"core":{"limit":60,"remaining":12,"reset":1791993785}}}').exhausted, false);
});

test('C4: the rate-limit message names a retry time or says later', () => {
  assert.match('GitHub 暫時限制查詢次數，請在 2026年10月15日 上午1:46:40 後再試', RATE_LIMITED);
  assert.match('GitHub 暫時限制查詢次數，請稍後再試', RATE_LIMITED);
  assert.doesNotMatch('操作失敗，請檢查網路後重試。', RATE_LIMITED);
});

test('C1: the daemon log keeps its lines but not codes, tokens or cookies', () => {
  const raw = 'a https://login.netbird.io/activate?user_code=ABCD-EFGH x\nAuthorization: Bearer eyJabc.def\ncookie: id=xyz; did=q\nSynoToken=tok1&x=1\nplain line';
  const out = redactLog(raw);
  for (const secret of ['ABCD-EFGH', 'eyJabc.def', 'xyz', 'tok1']) assert.ok(!out.includes(secret), secret);
  assert.equal(out.split('\n').length, 5);
  assert.ok(out.includes('plain line'));
});

test('synopkg status reads running or stopped from its JSON, whatever its exit code', () => {
  assert.equal(statusRunning('{"package":"SynoPkgConnect","status":"running"}'), true);
  assert.equal(statusRunning('{"package":"SynoPkgConnect","status":"stop"}'), false);
});

test('C4: alerts shown during a check are stale unless they name this check\'s retry time', () => {
  const now = 'GitHub 暫時限制查詢次數，請在 2026年10月15日 清晨5:04 後再試';
  const old = 'GitHub 暫時限制查詢次數，請在 2026年10月15日 凌晨2:03 後再試';
  assert.deepEqual(staleAlerts([old, now, now], '2026年10月15日 清晨5:04'), [old]);
  assert.deepEqual(staleAlerts([now], '2026年10月15日 清晨5:04'), []);
  assert.deepEqual(staleAlerts([], '2026年10月15日 清晨5:04'), []);
});
