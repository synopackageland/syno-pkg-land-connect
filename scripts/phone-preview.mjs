// The screens of the phone in each Overview route's Phone panel: the vendored
// phone browser page (vendor/mobile-web) on its Home and Apps tabs and with
// a few DSM Apps open, on a neutral demo NAS, in English, at 3x. A local
// server answers the DSM calls the page makes with fixed demo values; no NAS
// is contacted. Two folders of PNGs feed it:
//   - <icons-dir>: the App icons, named after the apps below (copy them from
//     any DSM's webman);
//   - <apps-dir>: one 1080 px wide capture per clickable App, named after it,
//     of that App's mobile-fit page on a DSM (scripts/phone-apps.mjs). A fake
//     warm DSM page shows it, so the phone page draws its own App top bar.
// From Home it also opens what Home links to: Security Advisor (the Needs
// attention row), the resources page (the Resources card) and Notifications
// (the bell tab) with a few demo notices of each level and each level's
// filter, and Storage.
// It writes one webp per screen and spots.json, where the phone's buttons
// sit in percent of the screen (PhoneFrame.vue places its buttons there):
// Home and spots.json into the bundle's assets (frontend/src/assets/phone),
// every other screen into frontend/public/phone, which the build copies as
// is and the phone loads only when it shows that screen. An output-dir
// takes all of them.
// Run after updating vendor/mobile-web:
//   node scripts/phone-preview.mjs <icons-dir> <apps-dir> [output-dir]
// or, for Home and the screens it links to only (no icons or App captures
// needed; the Apps screens and their spots stay as they are):
//   node scripts/phone-preview.mjs --home [output-dir]
// It needs Playwright's Chromium and cwebp.
import {createServer} from 'node:http'
import {execFileSync} from 'node:child_process'
import {existsSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync} from 'node:fs'
import {tmpdir} from 'node:os'
import {extname, join, resolve} from 'node:path'
import {fileURLToPath, URL} from 'node:url'
import {chromium} from 'playwright'

const repo = fileURLToPath(new URL('..', import.meta.url))
const web = join(repo, 'vendor/mobile-web')
const homeOnly = process.argv[2] === '--home'
if (!homeOnly && !process.argv[3]) throw new Error('usage: node scripts/phone-preview.mjs <icons-dir> <apps-dir> [output-dir] | --home [output-dir]')
const icons = homeOnly ? undefined : resolve(process.argv[2])
const shots = homeOnly ? undefined : resolve(process.argv[3])
const outArg = homeOnly ? process.argv[3] : process.argv[4]
const out = resolve(outArg ?? join(repo, 'frontend/src/assets/phone'))
const shelf = resolve(outArg ?? join(repo, 'frontend/public/phone'))
const PAGE = '/webman/3rdparty/SynoPkgConnect/m/'
const HOST = 'diskstation.local'
const GiB = 1024 ** 3
// The phone's screen in CSS px below its status bar, captured at 3x
const SCREEN = {width: 360, height: 736}
// Pixel width of each saved screen: about 2x of the Overview phone's 288 px screen
const SAVE_WIDTH = 540

// Demo values only: a model, two healthy drives, one volume about a third
// full, two Security Advisor findings, a recent backup and a few notices.
const drive = n => ({id: `sata${n}`, name: `Drive ${n}`, longName: `Drive ${n}`, num_id: n, order: n, overview_status: 'normal', smart_status: 'normal', status: 'normal', isSsd: false, temp: 36, size_total: String(4000 * GiB), container: {str: 'DS923+', type: 'internal', order: 0}})
const fail = (n = {}) => ({danger: 0, info: 0, outOfDate: 0, risk: 0, warning: 0, ...n})
const scan = (category, total, n) => ({category, fail: fail(n), failSeverity: n ? Object.keys(n)[0] : 'safe', progress: 100, runningItem: '', total, waitNum: 0})
const notice = (notifyId, time, title, level, params) => ({notifyId, time, title, level, className: '', msg: [JSON.stringify(params)]})
// The resources page draws a short history: each reading moves a little
let reading = 0
const wave = (base, swing, n) => Math.max(0, Math.round(base + swing * Math.sin(n / 1.7) + swing * .5 * Math.sin(n / .9)))
function utilization() {
  const n = reading++
  return {...demo.util,
    cpu: {...demo.util.cpu, user_load: wave(8, 4, n), system_load: wave(3, 1, n)},
    memory: {...demo.util.memory, real_usage: wave(41, 1, n)},
    network: [{device: 'total', rx: wave(182000, 70000, n + 2), tx: wave(46000, 20000, n + 4)}],
    disk: {...demo.util.disk, total: {...demo.util.disk.total, read_byte: wave(1250000, 500000, n + 1), write_byte: wave(380000, 150000, n + 3)}},
    time: demo.util.time + n * 5}
}
const demo = {
  util: {
    cpu: {user_load: 8, system_load: 3, other_load: 1, '1min_load': 20},
    memory: {real_usage: 41, total_real: 4 * 1024 * 1024, avail_real: 2.4 * 1024 * 1024, memory_size: 4 * 1024 * 1024},
    network: [{device: 'total', rx: 182000, tx: 46000}],
    disk: {total: {device: 'total', read_byte: 1250000, write_byte: 380000, utilization: 4}, disk: []},
    time: 1790000000,
  },
  overview: {model: 'DS923+', up_time: '312:10:4', sys_temp: 38, temperature_warning: false, ram_size: 4096, firmware_ver: 'DSM 7.2'},
  health: {disks: [drive(1), drive(2)], optime: '312:10:4', nif: [], temperature_warning: false, is_system_crashed: false},
  storage: {volumes: [{id: 'volume_1', vol_path: '/volume1', status: 'normal', summary_status: 'normal', fs_type: 'btrfs', size: {total: String(7200 * GiB), used: String(2450 * GiB)}}]},
  security: {items: {malware: scan('malware', 5), network: scan('network', 5), systemCheck: scan('systemCheck', 8, {warning: 1}), update: scan('update', 4), userInfo: scan('userInfo', 9, {warning: 1})}, lastScanTime: '1794531600', startTime: '', success: true, sysProgress: 100, sysStatus: 'warning'},
  backups: {task_list: [{task_id: 1, name: 'Photos to cloud', data_type: 'data', state: 'backupable', status: 'none', last_bkp_result: 'done', last_bkp_time: '2026/11/13 02:00:04', last_bkp_end_time: '2026/11/13 02:14:51', next_bkp_time: '2026/11/14 02:00', schedule: {schedule_enable: true}}], total: 1},
  notify: {items: [
    notice(6, 1794562800, 'demo:hyperbackup_failed', 'NOTIFICATION_ERROR', {'%TASK%': 'Documents to USB'}),
    notice(5, 1794531900, 'demo:security_warning', 'NOTIFICATION_WARN', {'%COUNT%': '2'}),
    notice(4, 1794514200, 'demo:package_update', 'NOTIFICATION_INFO', {'%COUNT%': '3'}),
    notice(3, 1794507240, 'demo:hyperbackup_done', 'NOTIFICATION_INFO', {'%TASK%': 'Photos to cloud'}),
    notice(2, 1794457800, 'demo:ups_battery', 'NOTIFICATION_WARN', {}),
    notice(1, 1794416400, 'demo:dsm_update', 'NOTIFICATION_INFO', {'%VERSION%': 'DSM 7.2.2'}),
  ], total: 6, newestMsgTime: 1794562800},
  // The notices' text, as DSM's string table gives it
  strings: {
    'demo:hyperbackup_failed': {title: 'Hyper Backup', msg: 'Backup task %TASK% failed: the destination is offline'},
    'demo:security_warning': {title: 'Security Advisor', msg: 'Security Advisor found %COUNT% warnings in the last scan'},
    'demo:package_update': {title: 'Package Center', msg: 'Updates are available for %COUNT% packages'},
    'demo:hyperbackup_done': {title: 'Hyper Backup', msg: 'Backup task %TASK% finished'},
    'demo:ups_battery': {title: 'UPS', msg: 'The UPS battery is low. Replace it soon'},
    'demo:dsm_update': {title: 'DSM', msg: '%VERSION% is ready to install'},
  },
  user: {Session: {user: 'admin', is_admin: true, hostname: 'DiskStation'}},
  lastRead: {},
}
// The App grid: DSM's own apps, each a built-in module so the page needs no
// package manifest. Icons are served from the icons folder by app id.
const APPS = {
  FileStation: 'File Station', ControlPanel: 'Control Panel', PackageCenter: 'Package Center', StorageManager: 'Storage Manager',
  ResourceMonitor: 'Resource Monitor', LogCenter: 'Log Center', SecurityAdvisor: 'Security Advisor', Drive: 'Drive Admin Console',
  ContainerManager: 'Container Manager', HyperBackup: 'Hyper Backup', VirtualMachineManager: 'Virtual Machine Manager', WebStation: 'Web Station',
}
// The Apps the phone opens, with the page each one reports
const OPEN = {StorageManager: 'Overview', FileStation: '', ControlPanel: ''}
const launchId = id => `SYNO.Demo.${id}`
const jsConfig = {JSConfig: Object.fromEntries(Object.entries(APPS).map(([id, title]) => [`webman/modules/${id}`, {[launchId(id)]: {type: 'app', title, icon: 'images/icon_{0}.png', jsBaseURL: `webman/modules/${id}`, allUsers: true}}]))}
function answer(api, type) {
  if (api === 'SYNO.Core.System.Utilization') return utilization()
  if (api === 'SYNO.Core.DSMNotify.Strings') return demo.strings
  if (api === 'SYNO.Core.System') return type === 'systemHealth' ? demo.health : demo.overview
  if (api === 'SYNO.Storage.CGI.Storage') return demo.storage
  if (api === 'SYNO.Core.SecurityScan.Status') return demo.security
  if (api === 'SYNO.Backup.Task') return demo.backups
  if (api === 'SYNO.Core.DSMNotify') return demo.notify
  if (api === 'SYNO.Core.Desktop.Initdata') return demo.user
  if (api === 'SYNO.Core.UserSettings') return demo.lastRead
  return undefined
}
const apis = ['SYNO.API.Auth', 'SYNO.Entry.Request', 'SYNO.Core.System.Utilization', 'SYNO.Core.System', 'SYNO.Storage.CGI.Storage', 'SYNO.Core.SecurityScan.Status', 'SYNO.Backup.Task', 'SYNO.Core.DSMNotify', 'SYNO.Core.DSMNotify.Strings', 'SYNO.Core.Desktop.Initdata', 'SYNO.Core.UserSettings']
const types = {'.html': 'text/html', '.js': 'text/javascript', '.png': 'image/png', '.ttf': 'font/ttf', '.webmanifest': 'application/manifest+json'}

// The warm DSM page the phone page opens DSM Apps in: it shows the App's
// capture and reports the App the way DSM's mobile fit does.
const warm = `<!doctype html><meta name="viewport" content="width=device-width"><title>DiskStation</title>
<style>html,body{margin:0;background:#fff}img{display:block;width:100%}</style><body><script>
var titles=${JSON.stringify(Object.fromEntries(Object.keys(OPEN).map(id => [launchId(id), APPS[id]])))}, pages=${JSON.stringify(Object.fromEntries(Object.entries(OPEN).map(([id, page]) => [launchId(id), page])))};
function launch(id){if(!titles[id])return false;document.body.innerHTML='<img src="/demo-app/'+id.split('.').pop()+'.png">';
var img=document.body.firstChild;img.onload=function(){window.ReactNativeWebView&&window.ReactNativeWebView.postMessage(JSON.stringify({internal:'fitApp',origin:location.origin,app:{id:id,title:titles[id],page:pages[id],menu:true,back:0}}))};return true}
Object.defineProperty(window,'__splFitLaunch',{value:launch});Object.defineProperty(window,'__splFitHome',{value:function(){document.body.innerHTML=''}});
</script>`

const server = createServer((req, res) => {
  const url = new URL(req.url, 'http://localhost')
  const json = value => { res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify(value)) }
  const png = file => { res.setHeader('Content-Type', 'image/png'); res.end(readFileSync(file)) }
  const icon = /^\/webman\/modules\/(\w+)\/images\/icon_\d+\.png$/.exec(url.pathname)
  if (icon && icons && APPS[icon[1]] && existsSync(join(icons, `${icon[1]}.png`))) return png(join(icons, `${icon[1]}.png`))
  const shot = /^\/demo-app\/(\w+)\.png$/.exec(url.pathname)
  if (shot && shots && OPEN[shot[1]] !== undefined) return png(join(shots, `${shot[1]}.png`))
  if (url.pathname === '/' && url.searchParams.has('launchApp')) { res.setHeader('Content-Type', 'text/html'); return res.end(warm) }
  if (url.pathname.startsWith(PAGE)) {
    const file = join(web, url.pathname.slice(PAGE.length) || 'index.html')
    if (file.startsWith(web) && existsSync(file) && statSync(file).isFile()) { res.setHeader('Content-Type', types[extname(file)] ?? 'application/octet-stream'); return res.end(readFileSync(file)) }
  }
  if (url.pathname === '/webapi/query.cgi') return json({success: true, data: {'SYNO.API.Auth': {path: 'entry.cgi', minVersion: 1, maxVersion: 7}}})
  if (url.pathname === '/webman/login.cgi') return json({success: true, SynoToken: 'demo'})
  if (url.pathname === '/webapi/entry.cgi') {
    const api = url.searchParams.get('api')
    if (api === 'SYNO.API.Info') return json({success: true, data: Object.fromEntries(apis.map(a => [a, {path: 'entry.cgi', minVersion: 1, maxVersion: 1}]))})
    if (api === 'SYNO.Core.Package') return json({success: true, data: {packages: []}})
    if (api === 'SYNO.Core.Desktop.Initdata' && url.searchParams.get('method') === 'get_ui_config') return json({success: true, data: jsConfig})
    if (api === 'SYNO.Entry.Request') {
      const calls = JSON.parse(url.searchParams.get('compound') || '[]')
      return json({success: true, data: {has_fail: false, result: calls.map(c => { const data = answer(c.api, c.type); return data ? {api: c.api, method: c.method, version: c.version, success: true, data} : {api: c.api, method: c.method, version: c.version, success: false, error: {code: 105}} })}})
    }
    if (api === 'SYNO.Core.Desktop.Initdata' && url.searchParams.get('method') === 'get_user_service') return json({success: true, data: {Session: {isLogined: true, is_admin: true, user: 'admin', lang: 'enu'}, AppPrivilege: {}, ...jsConfig}})
    const data = answer(api, url.searchParams.get('type'))
    return json(data ? {success: true, data} : {success: false, error: {code: 105}})
  }
  res.statusCode = 404
  res.end()
})

await new Promise(r => server.listen(0, '127.0.0.1', r))
const {port} = server.address()
const dir = mkdtempSync(join(tmpdir(), 'phone-preview-'))
const browser = await chromium.launch({args: [`--host-resolver-rules=MAP ${HOST} 127.0.0.1`]})
const save = async (page, name) => {
  await page.waitForTimeout(1200)
  const png = join(dir, `${name}.png`)
  await page.screenshot({path: png})
  const file = join(name === 'home' ? out : shelf, `${name}.webp`)
  execFileSync('cwebp', ['-quiet', '-q', '82', '-sharp_yuv', '-resize', String(SAVE_WIDTH), '0', png, '-o', file])
  console.log(file)
}
try {
  const page = await browser.newPage({viewport: SCREEN, deviceScaleFactor: 3, isMobile: true, hasTouch: true, locale: 'en-US', colorScheme: 'light'})
  // --home keeps the Apps spots it does not measure
  const spots = homeOnly ? JSON.parse(readFileSync(join(out, 'spots.json'), 'utf8')) : {}
  const spot = async (name, locator) => {
    const b = await locator.boundingBox()
    const pct = (v, of) => Math.round(v / of * 1000) / 10
    spots[name] = {left: pct(b.x, SCREEN.width), top: pct(b.y, SCREEN.height), width: pct(b.width, SCREEN.width), height: pct(b.height, SCREEN.height)}
  }
  // A press as a finger makes it: some controls measure on press, so down and up need a moment between
  const tap = async locator => {
    const box = await locator.boundingBox()
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
    await page.mouse.down(); await page.waitForTimeout(120); await page.mouse.up()
  }
  await page.goto(`http://${HOST}:${port}${PAGE}index.html`)
  await page.getByText('Security Advisor').waitFor()
  // Let the resources history fill before anything is captured
  await page.waitForTimeout(90000)
  await save(page, 'home')
  await spot('home', page.getByRole('tab').nth(0))
  await spot('apps', page.getByRole('tab').nth(1))
  await spot('notices', page.getByRole('tab').nth(2))
  // What Home links to: each opens over Home with a back arrow (top left)
  const linked = {security: page.getByRole('button', {name: /^Security Advisor, /}).first(), resources: page.getByRole('button', {name: 'NAS status', exact: true}).first(), storage: page.getByRole('button', {name: 'Storage', exact: true}).first()}
  for (const [name, link] of Object.entries(linked)) {
    await spot(name, link)
    await tap(link)
    const back = page.getByRole('button', {name: 'Back', exact: true}).first()
    await back.waitFor()
    await save(page, name)
    if (!spots.up) await spot('up', back)
    await tap(back)
    await page.waitForTimeout(1500)
  }
  await page.getByRole('tab').nth(2).click()
  await page.getByText('Backup task Documents to USB failed', {exact: false}).first().waitFor()
  await save(page, 'notices')
  // Its filters: each level on its own, then All again
  for (const [name, label] of [['errors', 'Errors'], ['warnings', 'Warnings'], ['info', 'Info'], ['all', 'All']]) {
    const chip = page.getByText(label, {exact: true}).filter({visible: true}).first()
    await spot(`notices-${name}`, chip)
    await tap(chip)
    if (name !== 'all') await save(page, `notices-${name}`)
  }
  // The Apps tab and the Apps it opens (not with --home)
  if (!homeOnly) {
    await page.getByRole('tab').nth(1).click()
    await page.getByText('Web Station').waitFor()
    await save(page, 'apps')
    for (const id of Object.keys(OPEN)) await spot(id, page.getByLabel(`Open ${APPS[id]}`, {exact: true}))
    for (const id of Object.keys(OPEN)) {
      await tap(page.getByLabel(`Open ${APPS[id]}`, {exact: true}))
      await page.waitForTimeout(4000)
      await save(page, `app-${id}`)
      const home = page.getByRole('button', {name: 'Home', exact: true}).first()
      if (!spots.back) await spot('back', home)
      await home.click()
      await page.getByText('Web Station').waitFor()
      await page.waitForTimeout(1500)
    }
  }
  writeFileSync(join(out, 'spots.json'), JSON.stringify(spots, null, 1) + '\n')
} finally {
  await browser.close()
  server.close()
  rmSync(dir, {recursive: true, force: true})
}
