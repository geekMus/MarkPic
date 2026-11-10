// 抓一张窗口截图：node tools\cdp\shot.mjs <out.png> [port]
//
// 和 eval.mjs 一样要靠 --remote-debugging-port 打开 CDP；截的是窗口里那块页面，
// 不含系统标题栏与阴影（窗口本身的自绘标题栏在页面里，所以它在截图里）。
import { writeFileSync } from 'node:fs'
import { connect } from './connect.mjs'

const [out, portArg] = process.argv.slice(2)
if (!out) {
  console.error('用法：node tools\\cdp\\shot.mjs <out.png> [port]')
  process.exit(2)
}
const { send, close } = await connect(portArg || '9333')
try {
  const shot = await send('Page.captureScreenshot', { format: 'png' })
  writeFileSync(out, Buffer.from(shot.data, 'base64'))
  console.log(`saved ${out}`)
} finally {
  close()
}