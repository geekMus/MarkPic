// 抓一张窗口截图：node tools\cdp\shot.mjs <out.png> [port] [selector]
//
// 和 eval.mjs 一样要靠 --remote-debugging-port 打开 CDP；截的是窗口里那块页面，
// 不含系统标题栏与阴影（窗口本身的自绘标题栏在页面里，所以它在截图里）。
//
// 第三个参数是 CSS 选择器：给了就只截那个元素（外扩 8px），用来量间距、看
// 单行排版，比整窗截图清楚得多。
import { writeFileSync } from 'node:fs'
import { connect } from './connect.mjs'

const [out, portArg, selector] = process.argv.slice(2)
if (!out) {
  console.error('用法：node tools\\cdp\\shot.mjs <out.png> [port] [selector]')
  process.exit(2)
}
const { send, close } = await connect(portArg || '9333')
try {
  let clip
  if (selector) {
    const rect = await send('Runtime.evaluate', {
      expression: `(() => {
        const el = document.querySelector(${JSON.stringify(selector)})
        if (!el) return null
        const r = el.getBoundingClientRect()
        return { x: r.x, y: r.y, width: r.width, height: r.height }
      })()`,
      returnByValue: true,
    })
    const box = rect.result?.value
    if (!box) {
      console.error(`没有找到 ${selector}`)
      process.exit(1)
    }
    const pad = 8
    clip = {
      x: Math.max(0, box.x - pad),
      y: Math.max(0, box.y - pad),
      width: box.width + pad * 2,
      height: box.height + pad * 2,
      scale: 2,
    }
  }
  const shot = await send('Page.captureScreenshot', clip ? { format: 'png', clip, captureBeyondViewport: true } : { format: 'png' })
  writeFileSync(out, Buffer.from(shot.data, 'base64'))
  console.log(`saved ${out}`)
} finally {
  close()
}