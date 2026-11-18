// Invoke the installed CLI against a registered, real NAS. No HTTP/executor stub.
import assert from 'node:assert/strict';
import {execFile} from 'node:child_process';
import {readFileSync} from 'node:fs';

export type Command = {cli: string[]; effect: string; examples?: Record<string, unknown>[]};
export const commands: Record<string, Command> = JSON.parse(readFileSync('generated/ctl.json', 'utf8')).commands;
export type Reply<T> = {ok: boolean; data?: T; error?: {code: string}};

export async function cli<T>(args: string[], input?: Record<string, unknown>): Promise<Reply<T>> {
  assert.ok(process.env.E2E_CTL_HOST, 'Set E2E_CTL_HOST to a registered test host');
  const executable = 'node_modules/@synopackageland/cli/dist/bin.js';
  const child = execFile(process.execPath, [executable, ...args, '--host', process.env.E2E_CTL_HOST!, '--json'],
    {timeout: 45_000, maxBuffer: 4 << 20});
  if (input) child.stdin!.end(JSON.stringify(input));
  else child.stdin!.end();
  const output = await new Promise<string>((resolve, reject) => {
    let stdout = '';
    child.stdout!.on('data', chunk => { stdout += chunk; });
    child.on('error', reject);
    child.on('close', () => resolve(stdout));
  });
  // Never include stdout in an assertion: status can contain private host data.
  return JSON.parse(output) as Reply<T>;
}

export async function ctl<T = Record<string, unknown>>(id: string, input: Record<string, unknown> = {}, flags: string[] = []): Promise<T> {
  const result = await cli<T>(['ctl', 'connect', ...commands[id].cli, '--input', '-', ...flags], input);
  assert.equal(result.ok, true, `${id}: ${result.error?.code ?? 'CLI refused'}`);
  return result.data as T;
}

export async function until<T>(read: () => Promise<T>, done: (value: T) => boolean, timeout = 90_000): Promise<T> {
  const deadline = Date.now() + timeout;
  for (;;) {
    const value = await read();
    if (done(value)) return value;
    assert.ok(Date.now() < deadline, 'operation did not reach the required state before the bounded deadline');
    await new Promise(resolve => setTimeout(resolve, 2000));
  }
}
