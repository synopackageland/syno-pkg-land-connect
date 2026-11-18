// Connect inside its DSM window: open it, switch pages, and read the backend
// the way the window does (same session cookie and SynoToken).
import assert from 'node:assert/strict';
import {execFile} from 'node:child_process';
import {writeFileSync} from 'node:fs';
import {promisify} from 'node:util';
import type {Locator, Page} from 'playwright';
import {dismissNotifications, dsmPage, openApp, openUntilLoaded, registerDsmSecrets, signIn} from '@synopackageland/cli/e2e';

export const APP_ID = 'SYNO.SDS.SynoPkgConnect.Instance';
const CGI = '/webman/3rdparty/SynoPkgConnect/cgi/api.cgi';

export type Envelope<T = unknown> = {success: boolean; data?: T; error?: {code?: string}};
export type Connection = {provider: string; state: string; phase?: string; code?: string; endpoints?: {url: string; public: boolean; localPort: number}[];
  userCode?: string; deadline?: string; loginUrl?: string; netbirdIP?: string; mappings?: unknown[]; mode?: string; hostname?: string;
  networkReady?: boolean; serviceIds?: string[]};
export type Binary = {product: string; current: string; runtimeVersion?: string; latest: string; previous: string;
  updateAvailable: boolean; checkedAt: string; phase: string; busy?: boolean; errorCode?: string; retryAt?: string};

export const root = (p: Page = dsmPage()): Locator => p.locator('.dsmapp-vue-root');
export const pageBody = (id: string, p: Page = dsmPage()): Locator => root(p).locator(`[data-page="${id}"]`);
/** Viewport Dialogs belong to AppRoot's floating portal, outside the mount root. */
export const dialog = (selector: string, p: Page = dsmPage()): Locator =>
  p.locator(`[data-ui-floating] ${selector}, [data-ui-portals] ${selector}`);
const openingTransient = (error: unknown): boolean => error instanceof Error &&
  (error.name === 'TimeoutError' || /Timeout \d+ms exceeded|net::ERR_ABORTED/.test(error.message));

/** Readiness polls may reopen a half-loaded desktop; only the test's overall deadline limits them. */
export async function readyDesktop(app: {open(path?: string): Promise<unknown>}, current = dsmPage,
  reopen = openUntilLoaded): Promise<void> {
  await reopen(app, '/');
  for (;;) {
    const p = current();
    try {
      await p.locator('#sds-desktop').waitFor({state: 'visible', timeout: 20_000});
      await p.locator('#sds-taskbar-tray-item-user').waitFor({state: 'visible', timeout: 20_000});
      return;
    } catch (error) {
      if (!openingTransient(error)) throw error;
      if (await p.locator('input[name="username"]').isVisible()) await signIn(app, 'admin');
      else await reopen(app, '/');
    }
  }
}

/** Opens Connect's DSM window (openApp has closed DSM's notification cards) and waits for its sidebar. */
export async function openConnect(app: {open(path?: string): Promise<unknown>}): Promise<void> {
  await readyDesktop(app);
  await registerDsmSecrets();
  await dismissNotifications();
  for (;;) {
    try {
      await openApp(app, APP_ID, 20_000);
      await root().locator('[data-nav-item]').first().waitFor({state: 'visible', timeout: 20_000});
      return;
    } catch (error) {
      if (!openingTransient(error)) throw error;
      await readyDesktop(app);
    }
  }
}

/** Selects a sidebar page by its visible title, e.g. 'NetBird' or '設定'. */
export async function go(title: string, id: string, p: Page = dsmPage()): Promise<Locator> {
  await root(p).locator('[data-nav-item]', {hasText: title}).first().click();
  const body = pageBody(id, p);
  await body.waitFor({state: 'visible'});
  return body;
}

export async function call<T>(method: string, params: Record<string, unknown> = {}, p: Page = dsmPage()): Promise<Envelope<T>> {
  return p.evaluate(async ({cgi, method, params}) => {
    const token = (window as unknown as {SYNO?: {SDS?: {Session?: {SynoToken?: string}}}}).SYNO?.SDS?.Session?.SynoToken;
    const response = await fetch(cgi, {method: 'POST', credentials: 'same-origin',
      headers: {'Content-Type': 'application/json', ...(token ? {'X-SYNO-TOKEN': token} : {})},
      body: JSON.stringify({method, params})});
    return response.json();
  }, {cgi: CGI, method, params});
}

export async function data<T>(method: string, params: Record<string, unknown> = {}, p: Page = dsmPage()): Promise<T> {
  const reply = await call<T>(method, params, p);
  if (!reply.success) throw new Error(`${method} failed: ${reply.error?.code}`);
  return reply.data as T;
}

export async function connections(p: Page = dsmPage()): Promise<Record<string, Connection>> {
  const status = await data<{connections?: Connection[]}>('connection.status', {}, p);
  return Object.fromEntries((status.connections ?? []).map(c => [c.provider, c]));
}

/** The Cloudflare setting a temporary tunnel replaces: settings.json's mode, next to the saved own domain. */
export type CloudflareSaved = {state: string; mode: string; hostname: string};
export const cloudflareSaved = (c?: Connection): CloudflareSaved =>
  ({state: c?.state ?? 'disconnected', mode: c?.mode ?? '', hostname: c?.hostname ?? ''});

/** Why the setting cannot be put back after a temporary tunnel, or false when it can. */
export function cloudflareUnrestorable(saved: CloudflareSaved): string | false {
  if (saved.state !== 'disconnected') return `Cloudflare is ${saved.state}, not stopped; a running tunnel is not restored`;
  // A token tunnel keeps its hostname only in settings.json and its token not at all once stopped.
  if (saved.mode === 'token') return 'a saved token tunnel cannot be restored without its token';
  if (saved.mode !== 'quick' && saved.mode !== 'domain') return 'there is no saved Cloudflare setting to restore';
  return false;
}

/** The addresses a connection offers, as its address cards show them. */
export const addressesOf = (c?: Partial<Connection>): string[] => (c?.endpoints ?? []).map(endpoint => endpoint.url);

/** C1: NetBird left failed with login_output_invalid, as after a timed-out sign-in and a package restart. */
export const netbirdStuck = (c?: Partial<Connection>): boolean => c?.state === 'failed' && c.code === 'login_output_invalid';

/** C3: the connection.status reply as if NetBird were connected with `ip`, other methods unchanged. */
export function withConnectedNetBird<T extends Envelope<{connections?: Partial<Connection>[]}>>(reply: T, ip: string): T {
  const connections = (reply.data?.connections ?? []).filter(c => c.provider !== 'netbird');
  connections.push({provider: 'netbird', state: 'connected', netbirdIP: ip, networkReady: true});
  return {...reply, data: {...reply.data, connections}};
}

/** Changing App state waits until no other acceptance run uses the test NAS (E2E_CONNECT_WRITE=1). */
export const writes = process.env.E2E_CONNECT_WRITE === '1';
export const readOnlyReason = 'App state changes need E2E_CONNECT_WRITE=1';

export async function grantClipboard(p: Page = dsmPage()): Promise<void> {
  await p.context().grantPermissions(['clipboard-read', 'clipboard-write'], {origin: new URL(p.url()).origin});
}
export const clipboard = (p: Page = dsmPage()): Promise<string> => p.evaluate(() => navigator.clipboard.readText());

/** Polls connection.status until the provider's state passes `ok`. */
export async function waitState(provider: string, ok: (state: string) => boolean, timeout = 90_000): Promise<string> {
  const deadline = Date.now() + timeout;
  for (;;) {
    const state = (await connections())[provider]?.state ?? 'disconnected';
    if (ok(state)) return state;
    if (Date.now() > deadline) throw new Error(`${provider} stayed ${state}`);
    await dsmPage().waitForTimeout(1_500);
  }
}

/** Runs a command on the NAS over the same SSH login as the tunnel; never through the browser. */
async function nas(...command: string[]): Promise<string> {
  const config = process.env.DSM_E2E_SSH_CONFIG;
  const {stdout} = await promisify(execFile)('ssh', [...(config ? ['-F', config] : []), '-o', 'BatchMode=yes', process.env.DSM_E2E_SSH || 'admin@nas.example', ...command],
    {timeout: 180_000, maxBuffer: 4 << 20});
  return stdout;
}
const synopkg = (verb: 'start' | 'status' | 'stop') => nas('/usr/syno/bin/synopkg', verb, 'SynoPkgConnect');
export const statusRunning = (json: string): boolean => JSON.parse(json).status === 'running';
/** synopkg status exits non-zero (17) for a stopped package, yet still prints its JSON. */
export const packageRunning = async (): Promise<boolean> =>
  statusRunning(await synopkg('status').catch((error: {stdout?: string}) => error.stdout || '{}'));
export const stopPackage = async (): Promise<void> => { await synopkg('stop'); };
/** Starts Connect and resolves when synopkg reports it running; returns the seconds it took. */
export async function startPackage(): Promise<number> {
  const started = Date.now();
  await synopkg('start');
  assert.ok(await packageRunning(), 'Connect runs after synopkg start');
  return (Date.now() - started) / 1000;
}

/** Connect's private state on the NAS (connectd's var/connect). */
export const CONNECT_DIR = '/var/packages/SynoPkgConnect/var/connect';
/** IPv6's own settings and its connection gate, relative to CONNECT_DIR. */
export const IPV6_FILES = ['ipv6.json', 'connections/ipv6/provider.json'];
/** Tailscale's own settings and its connection gate, relative to CONNECT_DIR. */
export const TAILSCALE_FILES = ['tailscale/settings.json', 'connections/tailscale/provider.json'];

/** Copies `files` (cp -p) into a new root-only backup directory on the NAS and returns it. */
export async function backupConnectFiles(files: string[]): Promise<string> {
  const dir = `/root/e2e-connect-backup-${Date.now()}`;
  const copies = files.map(f => `if [ -e '${CONNECT_DIR}/${f}' ]; then mkdir -p "$(dirname '${dir}/${f}')" && cp -p '${CONNECT_DIR}/${f}' '${dir}/${f}'; fi`);
  await nas(`umask 077 && mkdir '${dir}' && ${copies.join(' && ')}`);
  return dir;
}

/** Whether every file still matches its backup; a file missing from both matches. */
export async function connectFilesMatch(dir: string, files: string[]): Promise<boolean> {
  const checks = files.map(f => `{ if [ -e '${dir}/${f}' ]; then cmp -s '${dir}/${f}' '${CONNECT_DIR}/${f}'; else [ ! -e '${CONNECT_DIR}/${f}' ]; fi; }`);
  return nas(`${checks.join(' && ')} && echo same`).then(out => out.trim() === 'same', () => false);
}

/** Puts the backup back with the package stopped (a file the backup lacks is removed), then starts it. */
export async function restoreConnectFiles(dir: string, files: string[]): Promise<void> {
  await stopPackage();
  try {
    const steps = files.map(f => `if [ -e '${dir}/${f}' ]; then cp -p '${dir}/${f}' '${CONNECT_DIR}/${f}'; else rm -f '${CONNECT_DIR}/${f}'; fi`);
    await nas(steps.join(' && '));
  } finally {
    await startPackage();
  }
}
export const removeBackup = (dir: string): Promise<string> => nas('rm', '-rf', `'${dir}'`);

/** Whether the NAS gets any HTTP answer from `url` within 10 s. */
export const nasReaches = async (url: string): Promise<boolean> =>
  (await nas('curl', '-s', '-o', '/dev/null', '-m', '10', '-w', '%{http_code}', url).catch(() => '000')) !== '000';

/** Probe from the NAS itself: a proxy's error page is not control-plane readiness. */
export const tailscaleControlReachable = async (): Promise<boolean> =>
  (await nas('curl', '-f', '-s', '-o', '/dev/null', '-m', '10', '-w', '%{http_code}',
    'https://controlplane.tailscale.com/key?v=71').catch(() => '000')).trim() === '200';

/** C4: GitHub's core API quota from its /rate_limit reply, which itself costs nothing. */
export function githubQuota(json: string): {exhausted: boolean; resetAt: string} {
  const core = JSON.parse(json).resources.core;
  return {exhausted: core.remaining === 0, resetAt: new Date(core.reset * 1000).toISOString()};
}
/** The NAS's own GitHub quota: it shares the NAS's public IP with every other client there. */
export const nasGithubQuota = async () => githubQuota(await nas('curl', '-s', '-m', '15', 'https://api.github.com/rate_limit'));
/** C4: zh-TW binaries.rateLimited or binaries.rateLimitedLater. */
export const RATE_LIMITED = /^GitHub 暫時限制查詢次數，請(在 .+ 後再試|稍後再試)$/;

/** The distinct alerts a card showed during a check that do not name this check's retry time. */
export const staleAlerts = (alerts: string[], time: string): string[] => [...new Set(alerts.filter(alert => !alert.includes(time)))];

/** Masks sign-in codes, bearer tokens, cookies and SynoTokens, keeping every line. */
export const redactLog = (text: string): string => text
  .replace(/(user_code=)[^&\s]+/gi, '$1<redacted>')
  .replace(/(Bearer\s+)\S+/gi, '$1<redacted>')
  .replace(/^(\s*cookie:\s*).*$/gim, '$1<redacted>')
  .replace(/(SynoToken=)[^&\s]+/gi, '$1<redacted>');

/** C1 evidence: the end of Connect's daemon log, masked, saved private under .e2e/output. */
export async function saveDaemonLog(name: string): Promise<string> {
  const file = `.e2e/output/${name}`;
  writeFileSync(file, redactLog(await nas('tail', '-n', '400', '/var/packages/SynoPkgConnect/var/daemon/daemon.log')), {mode: 0o600});
  return file;
}
