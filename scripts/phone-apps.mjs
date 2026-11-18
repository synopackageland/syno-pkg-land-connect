// The DSM App screens behind the Overview phone's clickable Apps: each App's
// page on a real DSM, opened the way the phone browser page opens it (one
// App, DSM's launchApp, with the mobile fit script from the mobile repo), at
// 360 CSS px and 3x. Before each shot the page is made neutral: the NAS name
// becomes DiskStation, File Station's list shows demo folder names, and
// scroll bars are hidden. Look at every PNG before using it; the account's
// DSM language must be English.
//   DSM_URL=https://<nas>:5001 DSM_USER=<admin> DSM_PASSWORD=<password> \
//     node scripts/phone-apps.mjs <mobile-repo> <output-dir>
// DSM_PROXY (for example socks5://127.0.0.1:1080) reaches a NAS through a
// proxy. Credentials come only from the environment. Feed the output folder
// to scripts/phone-preview.mjs as <apps-dir>.
import {mkdirSync, readFileSync} from 'node:fs'
import {join, resolve} from 'node:path'
import {URL} from 'node:url'
import {chromium} from 'playwright'

const [repo, dir] = process.argv.slice(2)
const {DSM_URL, DSM_USER, DSM_PASSWORD, DSM_PROXY} = process.env
if (!repo || !dir || !DSM_URL || !DSM_USER || !DSM_PASSWORD) throw new Error('usage: DSM_URL=… DSM_USER=… DSM_PASSWORD=… node scripts/phone-apps.mjs <mobile-repo> <output-dir>')
const out = resolve(dir)
mkdirSync(out, {recursive: true})
const origin = new URL(DSM_URL).origin
// The fit script as the phone page injects it (generated from dsm-fit.js and dsm-fit.css)
const gen = readFileSync(join(resolve(repo), 'src/core/webview/mobile-fit/fit.gen.ts'), 'utf8')
const fit = JSON.parse(/export const fitSource = (".*");/s.exec(gen)[1])
// The page below the phone page's App top bar: 360 × 680 CSS px
const VIEW = {width: 360, height: 680}
// Each App: DSM's launch ID and its English title, which the fit reports
const APPS = {
  StorageManager: ['SYNO.SDS.StorageManager.Instance', 'Storage Manager'],
  FileStation: ['SYNO.SDS.App.FileStation3.Instance', 'File Station'],
  ControlPanel: ['SYNO.SDS.AdminCenter.Application', 'Control Panel'],
}
const FOLDERS = ['Documents', 'Photos', 'Projects', 'Backups', 'Music', 'Videos']

// Runs in the DSM page
/* global document, window, NodeFilter */
function neutral({folders, folder}) {
  const style = document.createElement('style')
  style.textContent = '*::-webkit-scrollbar{display:none!important}*{scrollbar-width:none!important}' +
    '.ps__rail-x,.ps__rail-y,.vscrollerbar,.vscrollerbase,.hscrollerbar,.hscrollerbase,.hscrollerbarbeg,.hscrollerbasebeg{display:none!important}'
  document.head.appendChild(style)
  const texts = root => { const w = document.createTreeWalker(root, NodeFilter.SHOW_TEXT), all = []; let n; while ((n = w.nextNode())) all.push(n); return all }
  // File Station: the folder names in the list
  let renamed = 0
  document.querySelectorAll('.x-grid3-row').forEach((row, i) => {
    const cell = row.querySelector('td')
    for (const n of cell ? texts(cell) : []) if (n.nodeValue.trim()) { renamed++; n.nodeValue = folders[i % folders.length] }
  })
  const host = (typeof window._S === 'function' && window._S('hostname')) || ''
  for (const n of texts(document.body)) {
    if (host && n.nodeValue.includes(host)) n.nodeValue = n.nodeValue.split(host).join('DiskStation')
    // The shared folder File Station opens on, in its path box
    if (folder && n.nodeValue.trim() === folder) n.nodeValue = 'home'
  }
  return {host: Boolean(host), folders: renamed}
}

const browser = await chromium.launch(DSM_PROXY ? {proxy: {server: DSM_PROXY}} : {})
try {
  const context = await browser.newContext({viewport: VIEW, deviceScaleFactor: 3, isMobile: true, hasTouch: true, locale: 'en-US', ignoreHTTPSErrors: true})
  // The phone page gives the fit a reporting channel; here it only records
  await context.addInitScript(`if (window.top === window) { window.__splFitFrame = true; window.__fit = []; window.ReactNativeWebView = {postMessage: d => window.__fit.push(String(d))};\n${fit}\n}`)
  const page = await context.newPage()
  const login = await (await page.request.post(`${origin}/webapi/entry.cgi`, {form: {api: 'SYNO.API.Auth', version: '6', method: 'login', account: DSM_USER, passwd: DSM_PASSWORD, session: 'webui', format: 'cookie'}})).json()
  if (!login.success) throw new Error(`DSM sign-in failed (code ${login.error?.code})`)
  for (const [name, [id, title]] of Object.entries(APPS)) {
    await page.goto(`${origin}/?launchApp=${encodeURIComponent(id)}`)
    // The fit reports the App once its window shows; DSM's own pages load for a while after that
    await page.waitForFunction(t => (window.__fit || []).some(m => m.includes('"fitApp"') && m.includes(JSON.stringify(t))), title, {timeout: 60000})
      .catch(() => { throw new Error(`${name}: no English "${title}" window; is the account's DSM language English?`) })
    await page.waitForTimeout(12000)
    // File Station opens on a shared folder, the page the fit reports
    const report = await page.evaluate(() => window.__fit.filter(m => m.includes('"fitApp"')).pop())
    const folder = name === 'FileStation' ? JSON.parse(report).app?.page || '' : ''
    const done = await page.evaluate(neutral, {folders: FOLDERS, folder})
    await page.waitForTimeout(500)
    await page.screenshot({path: join(out, `${name}.png`)})
    console.log(join(out, `${name}.png`), done)
  }
  await page.request.get(`${origin}/webapi/entry.cgi?api=SYNO.API.Auth&version=6&method=logout&session=webui`)
} finally {
  await browser.close()
}
