// Builds src/upload/wrapper.ts into the script the NAS injects into DSM pages
// (backend/internal/bigupload/upload.js). --check fails when that copy is stale.
import { transformWithEsbuild } from 'vite'
import { readFileSync, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const source = fileURLToPath(new URL('../src/upload/wrapper.ts', import.meta.url))
export const artifact = fileURLToPath(new URL('../../backend/internal/bigupload/upload.js', import.meta.url))

export async function buildUpload() {
  const result = await transformWithEsbuild(readFileSync(source, 'utf8'), 'wrapper.ts', {
    format: 'iife', target: 'es2020', minify: true, legalComments: 'none', charset: 'utf8',
    define: { __CONNECT_UPLOAD_AUTO__: 'true' },
    banner: '/* Syno Connect Center: large uploads through Cloudflare. Built from frontend/src/upload/wrapper.ts. */',
  })
  return result.code
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const code = await buildUpload()
  if (process.argv.includes('--check')) {
    if (readFileSync(artifact, 'utf8') !== code) throw new Error('backend/internal/bigupload/upload.js is stale; run pnpm run build:upload')
  } else {
    writeFileSync(artifact, code)
  }
}
