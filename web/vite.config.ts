import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// Built into the Go package that embeds it, so `go build` alone produces a binary with the interface in it.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  base: './',
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
    sourcemap: false,
    chunkSizeWarningLimit: 1200,
  },
  server: {
    proxy: { '/api': { target: 'http://127.0.0.1:8280', changeOrigin: false } },
  },
  test: { environment: 'happy-dom', exclude: ['e2e/**', 'node_modules/**'] },
})
