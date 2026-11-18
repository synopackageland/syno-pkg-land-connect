import {test} from 'node:test';
import assert from 'node:assert/strict';
import {execFileSync,spawnSync} from 'node:child_process';
import {chmodSync,mkdirSync,mkdtempSync,readFileSync,writeFileSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
const script='scripts/frontend-check.sh';
const env=extra=>{const base={...process.env};delete base.SYNOPKGLAND_DOCKER_SSH;return {...base,...extra};};
test('frontend-check runs the command in frontend/ here without SYNOPKGLAND_DOCKER_SSH',()=>{
 assert.equal(execFileSync('bash',[script,'pwd'],{encoding:'utf8',env:env({})}).trim(),join(process.cwd(),'frontend'));
 assert.equal(spawnSync('bash',[script,'exit 3'],{env:env({})}).status,3);
});
test('frontend-check runs the command in the Node.js image on the Docker host, in a gate slot, after pnpm install',()=>{
 const dir=mkdtempSync(join(tmpdir(),'frontend-check-'));
 mkdirSync(join(dir,'bin'));
 writeFileSync(join(dir,'bin','ssh'),`#!/bin/sh\nshift\nprintf '%s\\n' "$*" >>"${dir}/ssh.log"\ncat >/dev/null\n`);
 chmodSync(join(dir,'bin','ssh'),0o755);
 execFileSync('bash',[script,'pnpm test'],{stdio:['ignore','pipe','pipe'],env:env({PATH:`${dir}/bin:${process.env.PATH}`,
  SYNOPKGLAND_DOCKER_SSH:'host',SYNOPKGLAND_CONTAINER_PREFIX:'t-1',SYNOPKGLAND_DOCKER_NPM_REGISTRY:'https://mirror.invalid/'})});
 const run=readFileSync(join(dir,'ssh.log'),'utf8').split('\n').find(line=>line.includes('--name t-1-frontend'));
 assert.match(run,/synopkgland-gate\.slot\$slot\.lock/);
 assert.match(run,/library\/node:24\.20\.0-bookworm@sha256:[0-9a-f]{64} /);
 assert.match(run,/-e npm_config_registry=https:\/\/mirror\.invalid\//);
 assert.match(run,/-e COREPACK_NPM_REGISTRY=https:\/\/mirror\.invalid\//);
 assert.match(run,/-e COREPACK_HOME=\/cache\/npm\/corepack /);
 assert.match(run,/-v synopkgland-cache-npm:\/cache\/npm /);
 assert.match(run,/cd frontend && corepack enable --install-directory \/tmp\/bin pnpm && pnpm install --frozen-lockfile [^;']*&& pnpm test/);
 // vitest takes half the host's CPUs: the other gate slot has the rest.
 assert.match(run,/-e VITEST_MAX_WORKERS=\$\(\(\$\(nproc\) \/ 2\)\) /);
 // The e2e test's Go fixture is built here: the private SDK module is not there.
 assert.match(run,/-e CONNECT_DEV_FIXTURE=\/tmp\/src\/\.gate\/fixture /);
});
