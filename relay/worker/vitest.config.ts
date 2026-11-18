import { cloudflareTest } from '@cloudflare/vitest-plugin';
import { defineConfig } from 'vitest/config';
export default defineConfig({
  plugins: [cloudflareTest({
    wrangler: {configPath: './wrangler.jsonc'},
    miniflare: {bindings: {RELAY_SECRET: 'test-secret-that-is-at-least-32-bytes-long'}},
  })],
});
