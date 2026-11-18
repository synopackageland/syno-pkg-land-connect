import {execFileSync} from 'node:child_process';
import assert from 'node:assert/strict';
const files = execFileSync('gofmt', ['-l', 'backend'], {encoding:'utf8'});
assert.equal(files, '', `Run gofmt on:\n${files}`);
execFileSync('git', ['diff', '--check'], {stdio:'inherit'});

execFileSync('frontend/node_modules/.bin/eslint',['--config','frontend/eslint.config.js','scripts/*.mjs','tests/*.mjs'],{stdio:'inherit'});
execFileSync('python3',['-c',"import ast,pathlib; [ast.parse(p.read_text()) for folder in ['scripts','tests/build'] for p in pathlib.Path(folder).glob('*.py')]"],{stdio:'inherit'});
