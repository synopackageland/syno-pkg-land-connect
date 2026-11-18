import { build } from 'esbuild';
import { readFileSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
const directory = fileURLToPath(new URL('.', import.meta.url));
const artifact = new URL('../../backend/internal/relayworker/worker.js', import.meta.url);
const result = await build({
  absWorkingDir: directory, entryPoints: ['src/index.ts'], bundle: true, format: 'esm', target: 'es2022',
  external: ['cloudflare:workers'], legalComments: 'inline', write: false,
  banner: {js: '/*! Syno Connect Center relay, derived from hostc 88cdb41 (Apache-2.0). See relay/worker/NOTICE and LICENSE. */'},
});
const bytes = result.outputFiles[0].contents;
if (process.argv.includes('--check')) {
  if (!Buffer.from(bytes).equals(readFileSync(artifact))) throw new Error('embedded worker.js is stale; run npm run build');
} else {
  writeFileSync(artifact, bytes);
}
