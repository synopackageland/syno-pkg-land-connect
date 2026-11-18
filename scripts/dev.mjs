import {spawn,execFileSync} from 'node:child_process';
import {mkdtempSync,rmSync} from 'node:fs';
import {join} from 'node:path';
import {tmpdir} from 'node:os';
const dir=mkdtempSync(join(tmpdir(),'syno-connect-dev-'));
const binary=join(dir,'fixture');
execFileSync('go',['build','-o',binary,'./cmd/dev'],{cwd:'backend'});
const children=[spawn(binary,['-addr','127.0.0.1:19035','-data',dir,'-connections-fixture'],{stdio:'inherit'}),spawn(process.execPath,['node_modules/vite/bin/vite.js','--host','127.0.0.1','--port','19036','--strictPort'],{cwd:'frontend',stdio:'inherit'})];
let stopping=false;
async function stop(){if(stopping)return;stopping=true;await Promise.all(children.map(c=>new Promise(resolve=>{if(c.exitCode!==null||c.signalCode!==null)return resolve();c.once('exit',resolve);c.kill()})));rmSync(dir,{recursive:true,force:true});}
for(const signal of ['SIGINT','SIGTERM'])process.on(signal,()=>void stop());
for(const c of children)c.once('exit',()=>void stop());
