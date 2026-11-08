import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  base: './',
  plugins: [vue()],
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
    target: 'es2022',
  },
  server: {
    proxy: { '/api': 'http://127.0.0.1:17860' },
  },
  preview: {
    proxy: { '/api': 'http://127.0.0.1:17860' },
  },
})
