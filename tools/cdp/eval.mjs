// 在真窗口里跑一段 JS：node tools\cdp\eval.mjs "<expression>" [port]
//
// 窗口是 WebView2，开 CDP 的办法（启动前设环境变量）：
//   $env:WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS = '--remote-debugging-port=9333'
// 之后就能用这个脚本查 DOM、调 window.markpic、看布局——不用为了看一眼界面去开浏览器。
import { evaluate } from './connect.mjs'

const [expression, portArg] = process.argv.slice(2)
if  (!expression) {
  console.error('用法：node tools\\cdp\\eval.mjs "<expression>" [port]')
  process.exit(2)
}
const result = await evaluate(portArg || '9333', expression, { awaitPromise: true })
console.log(result)