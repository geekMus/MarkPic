import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 开发时后端端口由 scripts/web.ps1 通过环境变量 CMP_API 传进来，默认 17860。
// 打包后前端与 API 同源（都由 exe 提供），不经过这个代理。
const apiTarget = process.env.CMP_API ?? 'http://127.0.0.1:17860'

// 改 Go 代码时 watch-api.ps1 会重启后端，正在进行的 SSE 连接会被切断。
// http-proxy 默认把这种情况打成一整段红色报错栈，看着像出了故障，其实前端会自动重连。
// Vite 内部是在 configure 之后才挂自己的 error 监听，所以这里推迟一拍把它换成安静的处理器：
// 预期内的断连不吭声，其它错误照常打印。
function configureProxy(proxy: any) {
  setImmediate(() => {
    proxy.removeAllListeners('error')
    proxy.on('error', (err: NodeJS.ErrnoException, _req: unknown, res: any) => {
      if (err.code !== 'ECONNRESET' && err.code !== 'EPIPE') {
        console.error(`[vite] /api 代理出错：${err.message}`)
      }
      if (res && typeof res.writeHead === 'function' && !res.headersSent && !res.writableEnded) {
        res.writeHead(502, { 'Content-Type': 'text/plain; charset=utf-8' }).end('后端暂时不可用（可能正在重启）')
      }
    })
  })
}

const proxy = { '/api': { target: apiTarget, changeOrigin: false, configure: configureProxy } }

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
    watch: {
      // 有些编辑器（包括本仓库里用到的原子写入）保存文件时会先写
      //   .<文件名>.<pid>.<uuid>.tmpdir\<文件名>.tmp
      // 再改名到位。Node 的 watcher 在 Windows 上会因为这种「闪一下就没了」的目录
      // 抛 EBUSY，整个 dev server 直接退出。把这类临时目录排除掉，热更新才稳。
      ignored: ['**/*.tmpdir/**', '**/.*.tmpdir/**'],
    },
    proxy,
  },
  preview: {
    host: '127.0.0.1',
    port: 4173,
    strictPort: true,
    proxy,
  },
})
