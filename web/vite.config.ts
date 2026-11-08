import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 开发时后端端口由 scripts/dev.ps1 通过环境变量 CMP_API 传进来，默认 17860。
// 打包后前端与 API 同源（都由 exe 提供），不经过这个代理。
const apiTarget = process.env.CMP_API ?? 'http://127.0.0.1:17860'
const proxy = { '/api': { target: apiTarget, changeOrigin: false } }

export default defineConfig({
  // 相对路径，产物才能被 go:embed 后从任意端口/路径加载
  base: './',
  plugins: [vue()],
  build: {
    // 直接产出到 Go 的 go:embed 目录（internal/webui/webui.go）
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
    target: 'es2022',
  },
  server: {
    // 固定 IPv4 回环，别让 Windows 把 localhost 解析到 ::1 —— 文档和脚本里写的就是 127.0.0.1
    host: '127.0.0.1',
    port: 5173,
    strictPort: true,
    proxy,
  },
  preview: {
    host: '127.0.0.1',
    port: 4173,
    strictPort: true,
    proxy,
  },
})
