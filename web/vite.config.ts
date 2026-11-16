import { defineConfig, type Plugin } from 'vite'
import vue from '@vitejs/plugin-vue'

// 界面不再经过本地服务：产物会被摊到数据目录，由窗口直接以 file:// 打开。
// file:// 下的 ES module 会被 CORS 拦掉（页面 origin 是 null），所以打成一段
// 立即执行的传统脚本 —— 这样「双击 exe 打开窗口」不需要任何本地服务器。
//
// 产物已经是 iife 了，但 Vite 写 index.html 时仍会带上 type="module"；
// 那种 script 标签本身就会让浏览器按模块去加载（并因 file:// 的 CORS 失败），
// 所以这里把标签也改回传统脚本。
function classicScript(): Plugin {
  return {
    name: 'markpic-classic-script',
    // 只在 build 里跑：dev 模式下 Vite 自己注入的 <script type="module">（@vite/client 和
    // main.ts）同样会被上面的正则改成 defer，模块脚本退化成传统脚本就报
    // "Cannot use import statement outside a module"，窗口里一片空白。
    apply: 'build',
    enforce: 'post',
    transformIndexHtml(html) {
      return html
        .replace(/<script type="module" crossorigin /g, '<script defer ')
        .replace(/<script type="module" /g, '<script defer ')
        .replace(/<link rel="modulepreload"[^>]*>\s*/g, '')
    },
  }
}
export default defineConfig({
  // 相对路径：产物可能被摊到任意目录（带内容指纹的子目录）。
  base: './',
  plugins: [vue(), classicScript()],
  build: {
    // 直接产出到 Go 的 go:embed 目录（internal/ui/ui.go）
    outDir: '../internal/ui/dist',
    emptyOutDir: true,
    target: 'es2022',
    modulePreload: false,
    rollupOptions: {
      output: {
        format: 'iife',
        inlineDynamicImports: true,
      },
    },
  },
  server: {
    // 固定 IPv4 回环，别让 Windows 把 localhost 解析到 ::1 —— 脚本里写的就是 127.0.0.1
    host: '127.0.0.1',
    port: 5173,
    strictPort: true,
    watch: {
      // 有些编辑器（包括本仓库里用到的原子写入）保存文件时会先写
      //   .<文件名>.<pid>.<uuid>.tmpdir\\<文件名>.tmp
      // 再改名到位。Node 的 watcher 在 Windows 上会因为这种「闪一下就没了」的目录
      // 抛 EBUSY，整个 dev server 直接退出。把这类临时目录排除掉，热更新才稳。
      ignored: ['**/*.tmpdir/**', '**/.*.tmpdir/**'],
    },
  },
})