// Invoked by TestCustomGatePublicBrowser. Maps example.com through an isolated
// loopback HTTPS bridge; no public service, DSM account or Cloudflare resource.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import http from 'node:http';
import https from 'node:https';
import net from 'node:net';
import { chromium } from 'playwright';

const [endpoint, authority, provider] = process.argv.slice(2);
assert.match(endpoint, /^http:\/\/127\.0\.0\.1:\d+$/);
assert.equal(authority, 'media.example.com');
const publicOrigin = `https://${authority}`;
const signIn = '/_spl/connect/signin';
const input = [];
for await (const chunk of process.stdin) input.push(chunk);
const tls = JSON.parse(Buffer.concat(input).toString());
// Exercise the exact shipped Worker functions, not a duplicated allowlist.
const source = await readFile(new globalThis.URL('../backend/internal/relayworker/worker.js', import.meta.url), 'utf8');
assert.ok(source.includes('import { DurableObject } from "cloudflare:workers";'));
// DurableObject is never instantiated; only replace its platform import.
const nodeSource = source.replace('import { DurableObject } from "cloudflare:workers";', 'class DurableObject {}');
const worker = await import(`data:text/javascript;base64,${Buffer.from(nodeSource + '\nexport { forwardedRequestHeaders, publicResponseHeaders };').toString('base64')}`);
let lastPost;
let bridgeError;
const bridge = https.createServer(tls, async (req, res) => {
  try {
    if (['attacker.example.com', 'attacker.example.org'].includes(req.headers.host)) {
      res.writeHead(200, { 'Content-Type': 'text/html' });
      res.end(`<form method="post" action="${publicOrigin}${signIn}"><input name="account" value="alice"><input name="password" value="secret"><button>Send</button></form>`);
      return;
    }
    assert.equal(req.headers.host, authority);
    const headers = provider === 'workers'
      ? Object.fromEntries(worker.forwardedRequestHeaders(new globalThis.Headers(req.headers)))
      : { ...req.headers };
    // The relay client uses Config.Authority, not the visitor's Host. The
    // own-domain ingress preserves the public Host with no httpHostHeader.
    headers.host = authority;
    const upstream = http.request(endpoint + req.url, { method: req.method, headers }, response => {
      const pairs = [];
      for (let i = 0; i < response.rawHeaders.length; i += 2) pairs.push([response.rawHeaders[i], response.rawHeaders[i + 1]]);
      const out = provider === 'workers' ? worker.publicResponseHeaders(pairs) : new globalThis.Headers(pairs);
      if (req.method === 'POST') lastPost = { origin: headers.origin, fetchSite: headers['sec-fetch-site'], status: response.statusCode, location: out.get('location') };
      res.writeHead(response.statusCode, Object.fromEntries(out));
      response.pipe(res);
    });
    upstream.on('error', err => { bridgeError = err; res.destroy(err); });
    req.pipe(upstream);
  } catch (err) {
    bridgeError = err;
    res.destroy(err);
  }
});
await new Promise(resolve => bridge.listen(0, '127.0.0.1', resolve));
const proxy = http.createServer();
proxy.on('connect', (req, socket, head) => {
  if (![`${authority}:443`, 'attacker.example.com:443', 'attacker.example.org:443'].includes(req.url)) {
    socket.end('HTTP/1.1 403 Forbidden\r\n\r\n');
    return;
  }
  const target = net.connect(bridge.address().port, '127.0.0.1', () => {
    socket.write('HTTP/1.1 200 Connection Established\r\n\r\n');
    target.write(head);
    socket.pipe(target).pipe(socket);
  });
  socket.on('error', () => target.destroy());
  target.on('error', () => socket.destroy());
});
await new Promise(resolve => proxy.listen(0, '127.0.0.1', resolve));
let browser;
try {
  browser = await chromium.launch({ headless: true, proxy: { server: `http://127.0.0.1:${proxy.address().port}` } });
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await context.newPage();
  await page.goto(publicOrigin + signIn + '?next=%2Flibrary%3Fid%3D1');
  await page.locator('#account').fill('alice');
  await page.locator('#password').fill('wrong');
  await Promise.all([page.waitForResponse(r => r.request().method() === 'POST'), page.locator('button').click()]);
  console.log(JSON.stringify({ provider, attempt: 'wrong', ...lastPost }));
  assert.equal(lastPost.origin, publicOrigin, 'gate policy must retain the browser Origin');
  assert.equal(lastPost.fetchSite, 'same-origin');
  assert.equal(lastPost.status, 401, 'normal form must reach credential verification');

  await page.locator('#password').fill('secret');
  await Promise.all([page.waitForURL(publicOrigin + '/library?id=1'), page.locator('button').click()]);
  assert.equal(lastPost.status, 303);
  assert.equal(lastPost.location, '/library?id=1');
  assert.equal(await page.locator('body').innerText(), 'fixture App');
  const cookie = (await context.cookies(publicOrigin)).find(c => c.name === '__Host-spl-connect');
  assert.ok(cookie, 'browser must accept the cookie after Worker response handling');
  assert.equal(cookie.domain, authority);
  assert.equal(cookie.path, '/');
  assert.equal(cookie.secure, true);
  assert.equal(cookie.httpOnly, true);
  assert.equal(cookie.sameSite, 'Lax');

  for (const [attacker, fetchSite] of [['attacker.example.com', 'same-site'], ['attacker.example.org', 'cross-site']]) {
    await page.goto(`https://${attacker}/`);
    await Promise.all([page.waitForResponse(r => r.request().method() === 'POST'), page.locator('button').click()]);
    assert.equal(lastPost.origin, `https://${attacker}`);
    assert.equal(lastPost.fetchSite, fetchSite);
    assert.equal(lastPost.status, 403);
  }
  assert.equal(bridgeError, undefined);
  console.log(JSON.stringify({ provider, sameOrigin: 'passed', cookie: 'accepted', redirect: 'public host', crossOrigin: 'refused' }));
} finally {
  await browser?.close();
  await Promise.all([new Promise(resolve => proxy.close(resolve)), new Promise(resolve => bridge.close(resolve))]);
}
