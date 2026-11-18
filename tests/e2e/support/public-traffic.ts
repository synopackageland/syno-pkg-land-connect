// Public traffic must bypass the test host's cached/filtering company resolver.
// Pin only the destination address: retain the published hostname, SNI and TLS verification.
import {execFile} from 'node:child_process';
import {appendFileSync, mkdirSync, readFileSync, writeFileSync} from 'node:fs';
import {isIP} from 'node:net';
import {join} from 'node:path';
import {setTimeout as delay} from 'node:timers/promises';
import {promisify} from 'node:util';

const execute = promisify(execFile);
export type TrafficResponse = {completed: boolean; status: number; body: string};
export const servesFixture = (reply: TrafficResponse): boolean =>
  reply.completed && reply.status === 200 && reply.body.includes('connect-coverage-fixture');

export async function publicIPv4(hostname: string, directory: string): Promise<string[]> {
  mkdirSync(directory, {recursive: true, mode: 0o700});
  const answers = await Promise.all(['1.1.1.1', '8.8.8.8'].map(async server => {
    const args = [`@${server}`, hostname, 'A', '+time=3', '+tries=1', '+noall', '+comments', '+answer'];
    let stdout: string, stderr: string;
    try { ({stdout, stderr} = await execute('dig', args, {timeout: 5_000})); }
    catch (error) {
      const failure = error as {stdout?: string; stderr?: string};
      stdout = failure.stdout ?? ''; stderr = failure.stderr ?? '';
    }
    appendFileSync(join(directory, `dig-${server}.txt`), `${new Date().toISOString()} dig ${args.join(' ')}\n${stdout}${stderr}\n`, {mode: 0o600});
    return [...stdout.matchAll(/\sIN\s+A\s+([\d.]+)\s*$/gm)].map(match => match[1]).filter(ip => isIP(ip) === 4);
  }));
  return [...new Set(answers.flat())];
}

export async function requestPinned(address: string, ip: string, directory: string, label: string,
  userAgent = 'Mozilla/5.0'): Promise<TrafficResponse> {
  const url = new URL(address);
  if (!['https:', 'http:'].includes(url.protocol) || isIP(ip) !== 4) throw new Error('invalid public traffic target');
  mkdirSync(directory, {recursive: true, mode: 0o700});
  const prefix = join(directory, label.replace(/[^a-z0-9-]/gi, '-'));
  const bodyFile = `${prefix}.body`, headersFile = `${prefix}.headers`;
  writeFileSync(bodyFile, '', {mode: 0o600}); writeFileSync(headersFile, '', {mode: 0o600});
  url.searchParams.set('connect_coverage', `${Date.now()}-${label}`);
  const args = ['--noproxy', '*', '--resolve', `${url.hostname}:${url.port || (url.protocol === 'https:' ? '443' : '80')}:${ip}`,
    '--verbose', '--silent', '--show-error', '--connect-timeout', '5', '--max-time', '15',
    '--user-agent', userAgent, '--header', 'Cache-Control: no-cache', '--output', bodyFile,
    '--dump-header', headersFile, '--write-out', '%{http_code}', url.href];
  let stdout: string, stderr: string;
  let completed = true;
  try { ({stdout, stderr} = await execute('curl', args, {timeout: 20_000, maxBuffer: 4 << 20})); }
  catch (error) {
    const failure = error as {stdout?: string; stderr?: string};
    stdout = failure.stdout ?? ''; stderr = failure.stderr ?? ''; completed = false;
  }
  writeFileSync(`${prefix}.curl-v.txt`, `curl ${args.join(' ')}\n${stderr}\nHTTP status: ${stdout}\n`, {mode: 0o600});
  const status = Number(stdout.trim());
  return {completed: completed && status >= 100, status, body: readFileSync(bodyFile, 'utf8')};
}

export function publicTraffic(address: string, directory: string, userAgent: string) {
  const hostname = new URL(address).hostname;
  let addresses: string[] = [], sequence = 0;
  const request = async (phase: string): Promise<TrafficResponse> => {
    // Keep the last publicly resolved addresses for pause/remove too: DNS deletion
    // must not accidentally satisfy a negative traffic assertion.
    if (!addresses.length) throw new Error('public DNS has not resolved the published name');
    const ip = addresses[sequence % addresses.length];
    return requestPinned(address, ip, directory, `${phase}-${++sequence}`, userAgent);
  };
  const waitUntilServed = async (phase: string): Promise<boolean> => {
    // Propagation and tunnel restarts are polled; only the enclosing e2e deadline
    // limits this wait, not a separate short DNS/traffic wall-clock budget.
    while (!addresses.length) {
      addresses = await publicIPv4(hostname, directory);
      if (!addresses.length) await delay(5_000);
    }
    for (;;) {
      if (servesFixture(await request(phase))) return true;
      await delay(2_500);
    }
  };
  return {request, waitUntilServed};
}
