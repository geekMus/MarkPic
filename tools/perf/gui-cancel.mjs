// 取消路径的界面探针：1000 篇跑到一半点「取消」，量界面安定时间、行状态分布与页脚数字。
//
// 用 Playwright MCP 的 browser_run_code_unsafe 跑（filename 必须落在 C:\Users\mus 或
// C:\Users\mus\.playwright-mcp 下），把本文件复制过去再调用。
//
// 前置：exe 在 17982（CMP_DATA_DIR 指一个干净的数据目录，upload-cache.json 已删）、
//       假图床 mockpicgo 在 36678（建议 -delay 200ms 让一轮跑得够久）、
//       夹具 %TEMP%\cmp-gui-1000 里的 *.converted.md 已删掉。
async (page) => {
  const dir = 'C:\\Users\\mus\\AppData\\Local\\Temp\\cmp-gui-1000'
  const paths = []
  for (let i = 0; i < 1000; i += 1) paths.push(dir + '\\doc-' + String(i).padStart(4, '0') + '.md')
  const text = paths.join('\n')

  await page.setViewportSize({ width: 1280, height: 800 })

  await page.addInitScript(() => {
    // MCP 的 addInitScript 会在同一个 page 上累积，这里只挂一次。
    if (window.__sseHooked) return
    window.__sseHooked = true
    const Orig = window.EventSource
    const sse = { total: 0, byType: {} }
    window.__sse = sse
    function CountingES(...args) {
      const es = new Orig(...args)
      const origAdd = es.addEventListener.bind(es)
      es.addEventListener = (type, fn) => {
        const wrapped = (ev) => {
          sse.total += 1
          sse.byType[type] = (sse.byType[type] || 0) + 1
          return fn(ev)
        }
        return origAdd(type, wrapped)
      }
      return es
    }
    CountingES.prototype = Orig.prototype
    window.EventSource = CountingES
    window.__frames = []
    const tick = (ts) => {
      window.__frames.push(ts)
      window.requestAnimationFrame(tick)
    }
    window.requestAnimationFrame(tick)
  })

  await page.goto('http://127.0.0.1:17982', { waitUntil: 'load' })
  await page.waitForSelector('.foot__stats', { timeout: 20000 })
  await page.getByRole('button', { name: '手动输入路径' }).first().click()
  await page.waitForSelector('textarea', { timeout: 20000 })
  await page.waitForTimeout(600)

  await page.fill('textarea', text)
  await page.keyboard.press('Enter')
  await page.waitForFunction(
    () => (document.querySelector('.foot__stats')?.innerText ?? '').includes('1000'),
    null,
    { timeout: 60000, polling: 50 },
  )
  await page.waitForTimeout(800)

  const result = {}
  await page.evaluate(() => {
    window.__frames.length = 0
    window.__sse.total = 0
    window.__sse.byType = {}
  })

  const t0 = Date.now()
  await page.getByRole('button', { name: /开始转换/ }).click()
  await page.waitForFunction(
    () => [...document.querySelectorAll('button')].some((b) => /取消/.test(b.innerText)),
    null,
    { timeout: 20000, polling: 10 },
  )
  result.startToCancelButtonMs = Date.now() - t0

  // 让它跑一会儿，攒出「已完成的 + 正在传的 + 还没轮到的」三种行。
  await page.waitForTimeout(250)
  result.statsAtCancel = await page.evaluate(
    () => document.querySelector('.foot__stats')?.innerText ?? '',
  )

  // 直接点 DOM：运行中按钮每帧都在重渲染，Playwright 的稳定性检查会一直等到它消失。
  const t1 = Date.now()
  const clicked = await page.evaluate(() => {
    const btn = [...document.querySelectorAll('button')].find((b) => /取消/.test(b.innerText))
    if (!btn) return false
    btn.click()
    return true
  })
  result.clickedCancel = clicked
  if (!clicked) {
    result.note = '点取消时按钮已经没了：这一轮跑得太快，取消没测到'
    return JSON.stringify(result, null, 1)
  }
  await page.waitForFunction(
    () => ![...document.querySelectorAll('button')].some((b) => /取消/.test(b.innerText)),
    null,
    { timeout: 30000, polling: 10 },
  )
  result.cancelToSettleMs = Date.now() - t1

  await page.waitForTimeout(1500)

  const measured = await page.evaluate(() => {
    const labels = [
      '待转换', '上传中', '完成', '部分失败', '失败', '无图片',
      '已取消', '已结束', '未找到文件', '预览',
    ]
    const rows = [...document.querySelectorAll('.item')]
    const counts = {}
    for (const el of rows) {
      const text = el.querySelector('.item__status')?.innerText?.trim() ?? ''
      const hit = labels.find((l) => text.includes(l)) ?? (text || '(空)')
      counts[hit] = (counts[hit] || 0) + 1
    }
    const frames = window.__frames
    const gaps = []
    for (let i = 1; i < frames.length; i += 1) gaps.push(frames[i] - frames[i - 1])
    gaps.sort((a, b) => b - a)
    const buttons = [...document.querySelectorAll('button')].map((b) => b.innerText.trim())
    return {
      rows: rows.length,
      statusCounts: counts,
      frames: frames.length,
      worstGaps: gaps.slice(0, 6).map((g) => Math.round(g)),
      over100: gaps.filter((g) => g > 100).length,
      sseTotal: window.__sse.total,
      sseByType: window.__sse.byType,
      stats: document.querySelector('.foot__stats')?.innerText ?? '',
      hasStartButton: buttons.some((b) => /开始转换/.test(b)),
      hasCancelButton: buttons.some((b) => /取消/.test(b)),
      heap: performance.memory ? performance.memory.usedJSHeapSize : 0,
    }
  })

  Object.assign(result, measured)
  return JSON.stringify(result, null, 1)
}
