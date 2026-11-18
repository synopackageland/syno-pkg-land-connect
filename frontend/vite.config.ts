import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
export default defineConfig({
  plugins: [vue()],
  base: './',
  server: {proxy: {'/api.cgi': {target: 'http://127.0.0.1:19035', changeOrigin: false}}},
  // Library mode must replace Vue dependency env guards for the DSM browser.
  define: {'process.env.NODE_ENV': JSON.stringify('production')},
  build: {lib: {entry: 'src/main.ts', formats: ['es'], fileName: () => 'app.module.js'}, cssCodeSplit: false, rollupOptions: {output: {chunkFileNames: 'chunks/[name]-[hash].js', assetFileNames: asset => asset.names?.some(name => name.endsWith('.css')) ? 'app.css' : 'assets/[name]-[hash][extname]'}}},
})
