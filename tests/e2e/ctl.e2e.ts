import assert from 'node:assert/strict';
import {test} from '@e2e-dev/web';
import {cli, commands, ctl, until} from './support/ctl.ts';
import {connections, data, openConnect, tailscaleControlReachable, writes, readOnlyReason, type Binary, type Connection} from './support/connect.ts';

type Ports = {revision: number; entries: {id: string; port: number; enabled: boolean; protocol: string}[]};
type Access = {revision: number; emailCount: number; endpointCount: number; working: boolean};
type Status = {connections: Connection[]};
const options = {session: 'dsm', timeout: 240_000};
const writeOptions = {...options, skip: !writes && readOnlyReason};

test('ctl discovery matches every installed registry command', options, async () => {
  const reply = await cli<unknown>(['ctl', 'list', '--app', 'SynoPkgConnect', '--approve-installed']);
  assert.equal(reply.ok, true, reply.error?.code);
  const inventory = JSON.stringify(reply.data);
  for (const id of Object.keys(commands)) assert.ok(inventory.includes(id), `discovery omitted ${id}`);
});

for (const id of ['app.status', 'ctl.connection.status', 'ctl.ports.list', 'ctl.ipv6.status', 'advisor.recommend',
  'ctl.diagnose', 'external.status', 'ctl.cloudflare.access.status', 'integration.status']) {
  test(`real CLI read: ${id}`, options, async ({app}) => {
    await openConnect(app);
    const actual = await ctl(id);
    assert.ok(actual && typeof actual === 'object');
    if (id === 'app.status') {
      assert.equal(actual.package, 'SynoPkgConnect');
      assert.equal(actual.admin, true);
      assert.notEqual((actual.process as {uid: number}).uid, 0, 'App runs as the package account');
    }
    if (id === 'ctl.ports.list') {
      const browser = await data<Ports>('ports.list');
      assert.deepEqual((actual as Ports).entries.map(p => [p.id, p.port, p.enabled]), browser.entries.map(p => [p.id, p.port, p.enabled]));
    }
    if (id === 'ctl.connection.status') {
      const browser = Object.values(await connections()).map(p => [p.provider, p.state]).sort();
      assert.deepEqual((actual as Status).connections.map(p => [p.provider, p.state]).sort(), browser);
    }
    if (id === 'ctl.cloudflare.access.status') {
      const browser = await data<{emails: string[]; revision: number}>('cloudflare.access.get');
      assert.equal(actual.emailCount, browser.emails.length);
      assert.equal(actual.revision, browser.revision);
      assert.ok(!('emails' in actual), 'CLI does not disclose the email list');
    }
    if (id === 'ctl.ipv6.status') {
      const browser = await data<{state: string; reason: string; addresses: unknown[]}>('ipv6.status');
      assert.equal(actual.state, browser.state);
      assert.equal(actual.reason, browser.reason);
      assert.equal(actual.addressCount, browser.addresses.length);
      assert.ok(!('addresses' in actual), 'CLI omits NAS interface addresses');
    }
    if (id === 'integration.status') assert.deepEqual(actual, await data('integration.status'));
    if (id === 'external.status') {
      const browser = await data<{state: string}>('external.status');
      assert.equal(actual.state, browser.state);
    }
    if (id === 'advisor.recommend') {
      type Option = {id: string; rank: number; suitability: string};
      const browser = await data<{options: Option[]}>('advisor.recommend');
      const project = (options: Option[]) => options.map(o => [o.id, o.rank, o.suitability]);
      assert.deepEqual(project(actual.options as Option[]), project(browser.options));
      assert.equal((actual.options as Option[]).find(o => o.id === 'ipv6')?.suitability === 'recommended', false);
    }
    if (id === 'ctl.diagnose') {
      const current = await connections();
      for (const provider of actual.providers as {provider: string; state: string}[])
        assert.equal(provider.state, current[provider.provider]?.state ?? 'disconnected', provider.provider);
    }
  });
}

for (const [id, command] of Object.entries(commands).filter(([id]) => id.endsWith('.preview'))) {
  test(`real CLI preview and dry-run: ${id}`, options, async ({app}) => {
    await openConnect(app);
    const before = await data<Ports>('ports.list');
    const input = command.examples?.[0] ?? {};
    const preview = await ctl(id, input);
    assert.equal(preview.method, id.slice(0, -'.preview'.length));
    assert.equal(preview.requiresConfirmation, true);
    const plan = await cli(['ctl', 'connect', ...commands[preview.method as string].cli, '--input', '-', '--dry-run'], input);
    assert.equal(plan.ok, true, plan.error?.code);
    assert.deepEqual(await data('ports.list'), before, 'planning never changes service settings');
  });
}

test('ctl custom service create, edit, delete persist through CLI and browser readback', writeOptions, async ({app}) => {
  await openConnect(app);
  const before = await ctl<Ports>('ctl.ports.list');
  const port = Array.from({length: 1000}, (_, i) => 48000 + i).find(p => !before.entries.some(e => e.port === p))!;
  assert.ok(port);
  let id: string | undefined;
  try {
    const created = await ctl<Ports>('ctl.ports.upsert', {name: `Coverage ${Date.now()}`, protocol: 'http', port, enabled: false}, ['--yes']);
    id = created.entries.find(p => !before.entries.some(old => old.id === p.id))?.id;
    assert.match(id ?? '', /^custom-[a-f0-9]{32}$/);
    await ctl('ctl.ports.upsert', {id, name: 'Coverage edited', protocol: 'https', port, enabled: false}, ['--yes']);
    const browser = await data<Ports>('ports.list');
    assert.equal(browser.entries.find(p => p.id === id)?.protocol, 'https');
    await ctl('ctl.ports.delete', {id}, ['--yes']);
    assert.ok(!(await ctl<Ports>('ctl.ports.list')).entries.some(p => p.id === id));
    assert.ok(!(await data<Ports>('ports.list')).entries.some(p => p.id === id));
  } finally {
    // A lost create response is reconciled by the reserved, initially unused port.
    const remaining = (await ctl<Ports>('ctl.ports.list')).entries.find(p => p.id === id || p.port === port && !before.entries.some(old => old.id === p.id));
    if (remaining) await ctl('ctl.ports.delete', {id: remaining.id}, ['--yes']);
  }
});

test('ctl follow and DSM mapping toggles persist and restore with providers stopped', writeOptions, async ({app}) => {
  await openConnect(app);
  test.skip(Object.values(await connections()).some(c => c.state !== 'disconnected'), 'requires all providers stopped to avoid exposing existing services');
  const before = await data<Ports & {followServices: boolean}>('ports.list');
  const target = before.entries.find(p => p.id === 'dsm-https');
  assert.ok(target);
  try {
    await ctl('ctl.ports.followset', {enabled: !before.followServices, confirmExposure: true}, ['--yes']);
    assert.equal((await data<{followServices: boolean}>('ports.list')).followServices, !before.followServices);
    await ctl('ctl.ports.dsmset', {id: target.id, enabled: !target.enabled, confirmExposure: true}, ['--yes']);
    assert.equal((await data<Ports>('ports.list')).entries.find(p => p.id === target.id)?.enabled, !target.enabled);
  } finally {
    await ctl('ctl.ports.followset', {enabled: before.followServices, confirmExposure: true}, ['--yes']);
    await ctl('ctl.ports.dsmset', {id: target.id, enabled: target.enabled, confirmExposure: true}, ['--yes']);
    const after = await data<Ports & {followServices: boolean}>('ports.list');
    assert.equal(after.followServices, before.followServices);
    assert.deepEqual(after.entries, before.entries);
  }
});

test('ctl Access replacement and sync read back only the test-created email', writeOptions, async ({app}) => {
  await openConnect(app);
  const before = await ctl<Access>('ctl.cloudflare.access.status');
  test.skip(before.emailCount !== 0 || before.endpointCount !== 0 || before.working, 'requires empty Access settings and no published endpoints');
  try {
    await ctl('ctl.cloudflare.access.set', {emails: ['coverage@example.invalid'], revision: before.revision, confirm: true}, ['--yes']);
    const saved = await until(() => ctl<Access>('ctl.cloudflare.access.status'), s => !s.working);
    assert.equal(saved.emailCount, 1);
    assert.ok(saved.revision > before.revision);
    assert.deepEqual((await data<{emails: string[]}>('cloudflare.access.get')).emails, ['coverage@example.invalid']);
    await ctl('ctl.cloudflare.access.sync', {}, ['--yes']);
    assert.equal((await until(() => ctl<Access>('ctl.cloudflare.access.status'), s => !s.working)).emailCount, 1);
  } finally {
    const current = await until(() => ctl<Access>('ctl.cloudflare.access.status'), s => !s.working);
    if (current.emailCount) await ctl('ctl.cloudflare.access.set', {emails: [], revision: current.revision, confirm: true}, ['--yes']);
    assert.equal((await until(() => ctl<Access>('ctl.cloudflare.access.status'), s => !s.working)).emailCount, 0);
  }
});

test('ctl external set of the current hostname is a verified no-op', writeOptions, async ({app}) => {
  await openConnect(app);
  const before = await data<{hostname: string}>('external.get');
  const result = await ctl('external.set', {hostname: before.hostname}, ['--yes']);
  assert.equal(result.changed, false, 'DSM system settings must remain unchanged');
  assert.deepEqual(await data('external.get'), before);
});

for (const product of ['cloudflared', 'tailscale', 'netbird']) {
  test(`ctl binary check and verified installation: ${product}`, writeOptions, async () => {
    const before = await ctl<Binary>('binaries.status', {product});
    test.skip(!!before.current || !!before.busy, 'positive installation uses only a previously absent managed binary');
    await ctl('binaries.check', {product}, ['--yes', '--no-wait']);
    const checked = await until(() => ctl<Binary>('binaries.status', {product}), s => !s.busy, 120_000);
    assert.ok(checked.checkedAt && checked.checkedAt !== before.checkedAt, 'check recorded a new timestamp');
    assert.ok(checked.latest, 'check resolved an upstream version');
    await ctl('binaries.update', {product}, ['--yes', '--no-wait']);
    const installed = await until(() => ctl<Binary>('binaries.status', {product}), s => !s.busy, 180_000);
    assert.equal(installed.phase, 'ready');
    assert.equal(installed.current, checked.latest);
  });
}

test('ctl rollback without a retained version refuses and preserves the installed binary', writeOptions, async () => {
  const before = await ctl<Binary>('binaries.status', {product: 'tailscale'});
  test.skip(!!before.previous || !!before.busy, 'requires no retained binary');
  const result = await cli<unknown>(['ctl', 'connect', 'binaries', 'rollback', '--input', '-', '--yes', '--no-wait'], {product: 'tailscale'});
  const after = await until(() => ctl<Binary>('binaries.status', {product: 'tailscale'}), s => !s.busy);
  assert.ok(!result.ok || after.phase === 'failed', 'rollback must refuse instead of claiming completion');
  assert.equal(after.current, before.current);
  assert.ok(result.error?.code || after.errorCode, 'refusal has a diagnostic code');
});

for (const provider of ['tailscale', 'netbird']) {
  test(`ctl ${provider} advances to browser sign-in, checks, then stops`, writeOptions, async ({app}) => {
    if (provider === 'tailscale') test.skip(!await tailscaleControlReachable(), 'test NAS cannot reach the Tailscale control plane');
    await openConnect(app);
    const before = (await connections())[provider];
    test.skip(!!before && before.state !== 'disconnected', 'requires a disconnected provider');
    try {
      const accepted = await ctl<{state: string}>('ctl.connection.connect', {provider}, ['--yes']);
      assert.notEqual(accepted.state, 'connector-required', 'install prerequisite must succeed first');
      const login = await until(async () => (await ctl<Status>('ctl.connection.status')).connections.find(c => c.provider === provider)!, s => s?.state === 'needs-login');
      assert.equal(login.state, 'needs-login');
      const check = await ctl<{provider: string; externalVerified: boolean}>('connection.check', {provider});
      assert.equal(check.provider, provider);
      assert.equal(check.externalVerified, false, 'pending sign-in is not connected');
    } finally {
      await ctl('ctl.connection.disconnect', {provider}, ['--yes']);
      await until(async () => (await ctl<Status>('ctl.connection.status')).connections.find(c => c.provider === provider)?.state ?? 'disconnected', s => s === 'disconnected');
    }
  });
}

test('ctl forgetting a disconnected IPv6 fixture clears its service selection', writeOptions, async ({app}) => {
  await openConnect(app);
  const before = (await connections()).ipv6;
  test.skip(!!before && (before.state !== 'disconnected' || !!before.serviceIds?.length), 'requires an unconfigured disconnected IPv6 provider');
  await ctl('ctl.connection.forget', {provider: 'ipv6'}, ['--yes']);
  const after = (await connections()).ipv6;
  assert.equal(after?.state ?? 'disconnected', 'disconnected');
  assert.equal(after?.serviceIds?.length ?? 0, 0);
});
