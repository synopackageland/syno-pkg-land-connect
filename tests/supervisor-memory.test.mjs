import {test} from 'node:test';
import assert from 'node:assert/strict';
import {execFileSync} from 'node:child_process';

// The shared runtime budget adds only context/time and x/sys, without network code.
// Check the production import graph: unit-test heaps omit package init and
// cannot prove the always-on supervisor excludes provider/TLS dependencies.
test('resident supervisor excludes HTTP, TLS, providers and unrelated SDK packages',()=>{
 const deps=execFileSync('go',['list','-deps','./cmd/connectd'],{cwd:'backend',encoding:'utf8'}).trim().split('\n');
 const forbidden=deps.filter(p=>p==='net/http'||p.startsWith('crypto/')||(p.startsWith('golang.org/')&&!p.startsWith('golang.org/x/sys/'))||(p.startsWith('github.com/')&&!['supervision','internal/runtimebudget'].some(name=>p==='github.com/synopackageland/syno-pkg-land-sdk/'+name))||(p.startsWith('dsmapp/internal/')&&p!=='dsmapp/internal/daemon'));
 assert.deepEqual(forbidden,[],'resident connectd must only link its small lifecycle supervisor');
});
