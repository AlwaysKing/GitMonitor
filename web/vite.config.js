import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

const apiProxyTarget = process.env.VITE_API_PROXY_TARGET || 'http://127.0.0.1:8806'

export default defineConfig({
  plugins: [vue()],
  server: {
    host: process.env.VITE_HOST || '0.0.0.0',
    port: Number(process.env.VITE_PORT || process.env.PORT || 5173),
    proxy: {
      '/api': {
        target: apiProxyTarget,
        changeOrigin: true
      }
    }
  },
  preview: {
    host: process.env.VITE_HOST || '0.0.0.0',
    port: Number(process.env.VITE_PREVIEW_PORT || process.env.VITE_PORT || 4173)
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true
  }
})
