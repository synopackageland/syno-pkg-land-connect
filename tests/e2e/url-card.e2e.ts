// Address card and QR code: copy, open in a new tab and scan an address.
// Uses a running method's address, else starts a temporary Cloudflare tunnel,
// then puts the saved Cloudflare setting back and reads it back unchanged.
import {describe, test} from '@e2e-dev/web';
import assert from 'node:assert/strict';
import type {Locator} from 'playwright';
import {dsmPage} from '@synopackageland/cli/e2e';
import {addressesOf, call, clipboard, cloudflareSaved, cloudflareUnrestorable, connections, go, grantClipboard, openConnect, pageBody, readOnlyReason,
  waitState, writes} from './support/connect.ts';

describe('address card', {serial: true, session: 'dsm', timeout: 300_000}, () => {
  test('copy, open and QR show the same address', async ({agent, app}) => {
    await openConnect(app);
    await grantClipboard();
    let running = Object.values(await connections()).find(c => c.state === 'connected' && addressesOf(c).length);
    const temporary = !running;
    test.skip(temporary && !writes, `no connected address; ${readOnlyReason}`);
    const saved = cloudflareSaved((await connections()).cloudflare);
    if (temporary) {
      console.log('cloudflare before:', JSON.stringify(saved));
      const reason = cloudflareUnrestorable(saved);
      test.skip(reason !== false, `temporary Cloudflare tunnel skipped: ${reason}`);
    }
    try {
      if (temporary) {
        const body = await go('Cloudflare', 'cloudflare');
        await agent.act('On the Cloudflare page of Syno Connect Center, confirm the exposure checkbox and start a temporary (quick) Cloudflare tunnel. Do not use a token or a domain.');
        await waitState('cloudflare', s => s === 'connected', 180_000);
        running = (await connections()).cloudflare;
        await body.locator('[data-url-card]').first().waitFor({state: 'visible'});
      }
      const address = addressesOf(running)[0];
      assert.ok(address, `${running?.provider} is connected with an address`);
      const card = await cardFor(address);
      await card.getByRole('button', {name: '複製連結'}).click();
      assert.equal(await clipboard(), address);
      // The tab's first navigation, not where it lands: a new quick tunnel's
      // name may not resolve yet, and the error page has no origin.
      const dsmOrigin = new URL(dsmPage().url()).origin;
      const popup = dsmPage().waitForEvent('popup');
      const opened = dsmPage().context().waitForEvent('request', {timeout: 30_000,
        predicate: request => request.isNavigationRequest() && /^https?:/.test(request.url()) && new URL(request.url()).origin !== dsmOrigin});
      await card.locator('[data-url-open]').click();
      assert.equal(new URL((await opened).url()).origin, new URL(address).origin);
      await (await popup).close();
      const qrButton = card.locator('[data-url-qr]');
      await qrButton.click();
      assert.equal(await qrButton.getAttribute('aria-expanded'), 'true');
      assert.equal(await card.locator('svg[data-qr]').getAttribute('aria-label'), `此網址的 QR 碼：${address}`);
      await qrButton.click();
      assert.equal(await card.locator('svg[data-qr]').count(), 0);
    } finally {
      if (temporary && writes) {
        await go('Cloudflare', 'cloudflare');
        await pageBody('cloudflare').locator('[data-stop]').click();
        await waitState('cloudflare', s => s === 'disconnected');
        await restoreCloudflare(saved);
      }
    }
  });
});

/** The overview's card for an address, which every running method shows. */
async function cardFor(address: string): Promise<Locator> {
  const cards = (await go('總覽', 'status')).locator('[data-url-card]');
  await cards.first().waitFor({state: 'visible', timeout: 30_000});
  const index = await cards.evaluateAll((els, url) => els.findIndex(el =>
    [...el.querySelectorAll('input')].some(input => input.value === url) || el.textContent?.includes(url)), address);
  assert.ok(index >= 0, `the overview shows a card for ${address}`);
  return cards.nth(index);
}

/** A temporary tunnel saves mode quick; reconnecting the own domain and stopping saves mode domain again. */
async function restoreCloudflare(saved: ReturnType<typeof cloudflareSaved>): Promise<void> {
  if (saved.mode === 'domain' && cloudflareSaved((await connections()).cloudflare).mode !== 'domain') {
    const reply = await call('connection.connect', {provider: 'cloudflare', mode: 'domain', confirmExposure: true});
    assert.ok(reply.success, `reconnecting the own domain: ${reply.error?.code}`);
    await waitState('cloudflare', s => s === 'connected', 180_000);
    assert.ok((await call('connection.disconnect', {provider: 'cloudflare'})).success, 'stopping the own domain');
    await waitState('cloudflare', s => s === 'disconnected');
  }
  const after = cloudflareSaved((await connections()).cloudflare);
  console.log('cloudflare after:', JSON.stringify(after));
  assert.deepEqual(after, saved, 'the saved Cloudflare setting reads back unchanged');
}
