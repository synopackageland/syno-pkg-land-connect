// Settings (設定): one card per connector in three columns, each naming the
// version in use, with the "check updates" buttons aligned at the bottom.
import {describe, test} from '@e2e-dev/web';
import assert from 'node:assert/strict';
import {dsmPage} from '@synopackageland/cli/e2e';
import {RATE_LIMITED, data, staleAlerts, go, nasGithubQuota, openConnect, readOnlyReason, writes, type Binary, type Envelope} from './support/connect.ts';

const PRODUCTS = ['cloudflared', 'tailscale', 'netbird'];

describe('settings page', {serial: true, session: 'dsm', timeout: 180_000}, () => {
  test('three connector cards side by side name the version in use', async ({app}) => {
    await openConnect(app);
    // Record every tag from mounting on, to catch 尚未下載 before the status answers.
    await dsmPage().evaluate(() => {
      const seen: string[] = [];
      (window as unknown as {binaryTags: string[]}).binaryTags = seen;
      new MutationObserver(() => {
        for (const tag of document.querySelectorAll('.dsmapp-vue-root [aria-label] [data-binary-in-use]'))
          seen.push(`${tag.closest('[aria-label]')!.getAttribute('aria-label')}: ${(tag as HTMLElement).innerText.trim()}`);
      }).observe(document.body, {childList: true, subtree: true, characterData: true});
    });
    const body = await go('設定', 'settings');
    const boxes: {x: number; y: number; width: number; button: number}[] = [];
    for (const product of PRODUCTS) {
      const card = body.locator(`[aria-label="${product}"]`);
      const tag = card.locator('[data-binary-in-use]');
      await tag.waitFor({state: 'visible', timeout: 30_000});
      const status = await data<Binary>('binaries.status', {product});
      const version = status.runtimeVersion || status.current;
      // The tag says 尚未下載 until the card's own status read answers.
      const first = (await tag.innerText()).trim();
      const deadline = Date.now() + 15_000;
      let shown = first;
      while (version && shown !== `使用中 ${version}` && Date.now() < deadline) {
        await dsmPage().waitForTimeout(250);
        shown = (await tag.innerText()).trim();
      }
      if (first !== shown) console.log(`${product} tag first said ${first}, then ${shown}`);
      if (version) assert.equal(shown, `使用中 ${version}`);
      const tags = await dsmPage().evaluate(() => (window as unknown as {binaryTags: string[]}).binaryTags);
      if (version) assert.ok(!tags.includes(`${product}: 尚未下載`), `${product} never says 尚未下載 while it loads: ${[...new Set(tags)]}`);
      else assert.ok(!shown.includes('使用中'), `${product} is not installed`);
      const box = (await card.boundingBox())!;
      boxes.push({...box, button: (await card.getByRole('button', {name: '檢查更新'}).boundingBox())!.y});
    }
    console.log('tags while loading:', [...new Set(await dsmPage().evaluate(() => (window as unknown as {binaryTags: string[]}).binaryTags))]);
    // Cards in one row align their "check updates" buttons at the bottom.
    for (const row of new Set(boxes.map(b => Math.round(b.y)))) {
      const buttons = boxes.filter(b => Math.round(b.y) === row).map(b => b.button);
      assert.ok(Math.max(...buttons) - Math.min(...buttons) < 2, `check buttons align in a row: ${buttons}`);
    }
    // Three columns whenever the grid fits three 360px cards (CardGrid min-item-width).
    const grid = (await body.locator(`[aria-label="cloudflared"]`).locator('xpath=..').boundingBox())!;
    const gap = boxes[1].y === boxes[0].y ? boxes[1].x - boxes[0].x - boxes[0].width : 16;
    const columns = new Set(boxes.filter(b => Math.round(b.y) === Math.round(boxes[0].y)).map(b => b.x)).size;
    console.log(`settings grid ${Math.round(grid.width)}px wide shows ${columns} columns`);
    if (grid.width >= 3 * 360 + 2 * gap) assert.equal(columns, 3);
  });
});

describe('checking for updates', {serial: true, session: 'dsm', timeout: 180_000, skip: !writes && readOnlyReason}, () => {
  // With GitHub's quota used up the card says so and until when, not "check the network".
  test('checking NetBird for updates records a new check time, or names GitHub\'s limit', async ({app}) => {
    await openConnect(app);
    const body = await go('設定', 'settings');
    const card = body.locator('[aria-label="netbird"]');
    // The card's own first status read shows the previous result; watch only from after it.
    await card.locator('[data-binary-in-use]').waitFor({state: 'visible', timeout: 30_000});
    const before = await data<Binary>('binaries.status', {product: 'netbird'});
    const quota = await nasGithubQuota();
    console.log('NAS GitHub quota:', JSON.stringify(quota));
    console.log('before the check:', JSON.stringify({errorCode: before.errorCode, retryAt: before.retryAt, alerts: await card.getByRole('alert').allInnerTexts()}));
    // What the click's own request answered, to tell a refused call from the card's text.
    const reply = dsmPage().waitForResponse(response => response.request().postData()?.includes('"binaries.check"') ?? false, {timeout: 30_000})
      .then(response => response.json() as Promise<Envelope>).then(json => json.success ? 'ok' : json.error?.code ?? 'failed', () => 'no reply');
    // From the click on, the card says 檢查中 at once and shows no previous check's error.
    await card.evaluate(element => {
      const seen = {checking: false, alerts: [] as string[]};
      (window as unknown as {checkSeen: typeof seen}).checkSeen = seen;
      const record = () => {
        if ((element as HTMLElement).innerText.includes('檢查中')) seen.checking = true;
        const alert = element.querySelector('[role=alert]') as HTMLElement | null;
        if (alert) seen.alerts.push(alert.innerText.trim());
      };
      new MutationObserver(record).observe(element, {childList: true, subtree: true, characterData: true});
    });
    await card.getByRole('button', {name: '檢查更新'}).click();
    const busyShown = await card.getByText('檢查中').first().isVisible();
    const alertOnClick = await card.getByRole('alert').count();
    console.log(`right after the click: 檢查中 ${busyShown}, alerts ${alertOnClick}`);
    const seen = () => dsmPage().evaluate(() => (window as unknown as {checkSeen: {checking: boolean; alerts: string[]}}).checkSeen);
    if (quota.exhausted) {
      // binaries.check answers while the check still runs; the card shows the previous check's error until it ends.
      const answered = await reply;
      const deadline = Date.now() + 60_000;
      let after = await data<Binary>('binaries.status', {product: 'netbird'});
      while ((after.busy || after.checkedAt === before.checkedAt) && Date.now() < deadline) {
        await dsmPage().waitForTimeout(1_500);
        after = await data<Binary>('binaries.status', {product: 'netbird'});
      }
      assert.ok(!after.busy && after.checkedAt !== before.checkedAt, `the check ended: ${JSON.stringify(after)}`);
      const time = after.retryAt ? await dsmPage().evaluate(at =>
        new Intl.DateTimeFormat('zh-TW', {dateStyle: 'medium', timeStyle: 'short'}).format(new Date(at)), after.retryAt) : '';
      const alert = card.getByRole('alert');
      await alert.waitFor({state: 'visible', timeout: 60_000});
      const shownBy = Date.now() + 15_000;
      let shown = (await alert.innerText()).trim();
      while (!shown.includes(time) && Date.now() < shownBy) {
        await dsmPage().waitForTimeout(250);
        shown = (await alert.innerText()).trim();
      }
      await dsmPage().screenshot({path: '.e2e/output/c4-github-rate-limited.png'});
      console.log('rate-limited card says:', shown, 'retryAt', after.retryAt);
      assert.match(shown, RATE_LIMITED, `binaries.check answered ${answered}`);
      assert.ok(shown.includes(time), `the card names this check's retry time ${time}`);
      const during = await seen();
      console.log('during the check:', JSON.stringify({checking: during.checking, alerts: [...new Set(during.alerts)]}));
      assert.ok(during.checking, 'the card said 檢查中 during the check');
      assert.deepEqual(staleAlerts(during.alerts, time), [], 'the card never showed a previous check\'s error');
      return;
    }
    const deadline = Date.now() + 60_000;
    let after = before;
    while (after.checkedAt === before.checkedAt && Date.now() < deadline) {
      await new Promise(resolve => setTimeout(resolve, 1_500));
      after = await data<Binary>('binaries.status', {product: 'netbird'});
    }
    assert.ok(Date.parse(after.checkedAt) > Date.parse(before.checkedAt || '1970-01-01'), 'checkedAt advanced');
    assert.equal(after.current, before.current, 'checking does not change the installed version');
    const during = await seen();
    assert.ok(during.checking, 'the card said 檢查中 during the check');
    assert.deepEqual([...new Set(during.alerts)], [], 'a successful check shows no error');
  });
});
