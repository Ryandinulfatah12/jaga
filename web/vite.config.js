// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import tailwindcss from '@tailwindcss/vite'

// `npm run dev` proxies the API to a running `jaga start` (JAGA_DEV_API, default :8080).
const api = process.env.JAGA_DEV_API ?? 'http://127.0.0.1:8080'

export default defineConfig({
  plugins: [svelte(), tailwindcss()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    assetsInlineLimit: 0, // no data: URLs, so the CSP can stay at img-src 'self'
    sourcemap: false,
  },
  server: {
    proxy: {
      '/api': { target: api, changeOrigin: false },
      '/live': { target: api, changeOrigin: false, ws: true },
    },
  },
})
