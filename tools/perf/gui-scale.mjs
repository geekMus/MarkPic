// 界面在超大语料下的表现：入队/预览耗时、跑完耗时、掉帧、SSE 条数、JS 堆。
// 用法（Playwright MCP）：先把底下 count/dir/端口对齐，再
//   playwright__browser_run_code_unsafe { filename: "<绝对路径>\tools\perf\gui-scale.mjs" }
// 夹具用 tools/bench 造：-files <count> -refs 10 -pool 60 -kb 4 -prose 2 -dryrun -keep
async (page) => {
  const dir = 'C:\\Users\\mus\\AppData\\Local\\Temp\\cmp-gui-5000'
  const count = 5000
  const paths = []
  for (let i = 0; i < count; i += 1) paths.push(dir + '\\doc-' + String(i).padStart(4, '0') + '.md')
  const text = paths.join('\n')

  await page.setViewportSize({ width: 1280, height: 800 })

  await page.addInitScript(() => {
    // 注意：addInitScript 会在同一 page 上累积，重复跑探针前要 browser_close。
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
  await page.waitForTimeout(800)

  const result = { count }
  result.heapBefore = await page.evaluate(() =>
    performance.memory ? performance.memory.usedJSHeapSize : 0,
  )

  const tQueue0 = Date.now()
  await page.evaluate(() => {
    window.__frames.length = 0
  })
  // 注意：不要用 page.fill —— 它往 textarea 灌 15 万个字符时要花 20 多秒（平方级，
  // 是 Playwright 自己的开销，不是应用的）。这里直接赋值 + 派发 input，
  // 让 v-model 走同一条路径，量到的才是应用自己的反应。
  result.fillMs = await page.evaluate((value) => {
    const el = document.querySelector('textarea')
    const t = performance.now()
    el.value = value
    el.dispatchEvent(new Event('input', { bubbles: true }))
    return Math.round(performance.now() - t)
  }, text)
  await page.keyboard.press('Enter')
  const tReady0 = Date.now()
  await page.waitForFunction(
    (n) => (document.querySelector('.foot__stats')?.innerText ?? '').includes(String(n)),
    count,
    { timeout: 40000, polling: 50 },
  )
  result.afterInputMs = Date.now() - tReady0
  result.queueMs = Date.now() - tQueue0
  result.queueFrames = await page.evaluate(() => {
    const frames = window.__frames
    const gaps = []
    for (let i = 1; i < frames.length; i += 1) gaps.push(frames[i] - frames[i - 1])
    const sorted = gaps.slice().sort((a, b) => b - a)
    return {
      frames: frames.length,
      spanMs: frames.length > 1 ? Math.round(frames[frames.length - 1] - frames[0]) : 0,
      worstGaps: sorted.slice(0, 6).map((g) => Math.round(g)),
      over100: gaps.filter((g) => g > 100).length,
    }
  })
  await page.waitForTimeout(600)
  result.statsAfterPreview = await page.evaluate(
    () => document.querySelector('.foot__stats')?.innerText ?? '',
  )
  result.rowsRendered = await page.evaluate(
    () => document.querySelectorAll('.panel__body.queue__body .item').length,
  )
  result.rowHeight = await page.evaluate(() => {
    const el = document.querySelector('.panel__body.queue__body .item')
    return el ? Math.round(el.getBoundingClientRect().height * 100) / 100 : 0
  })
  result.scrollHeight = await page.evaluate(
    () => document.querySelector('.panel__body.queue__body')?.scrollHeight ?? 0,
  )

  await page.evaluate(() => {
    window.__frames.length = 0
    window.__sse.total = 0
    window.__sse.byType = {}
  })

  const t0 = Date.now()
  await page.getByRole('button', { name: /开始转换/ }).click()
  let sawCancel = false
  try {
    await page.waitForFunction(
      () => [...document.querySelectorAll('button')].some((b) => /取消/.test(b.innerText)),
      null,
      { timeout: 30000, polling: 10 },
    )
    sawCancel = true
  } catch (err) {
    sawCancel = false
  }
  result.sawCancel = sawCancel
  await page.waitForFunction(
    () => ![...document.querySelectorAll('button')].some((b) => /取消/.test(b.innerText)),
    null,
    { timeout: 240000, polling: 20 },
  )
  result.settleMs = Date.now() - t0
  await page.waitForTimeout(1000)

  const measured = await page.evaluate(() => {
    const frames = window.__frames
    const gaps = []
    for (let i = 1; i < frames.length; i += 1) gaps.push(frames[i] - frames[i - 1])
    const sorted = gaps.slice().sort((a, b) => b - a)
    return {
      frames: frames.length,
      windowMs: frames.length > 1 ? Math.round(frames[frames.length - 1] - frames[0]) : 0,
      worstGaps: sorted.slice(0, 6).map((g) => Math.round(g)),
      over50: gaps.filter((g) => g > 50).length,
      over100: gaps.filter((g) => g > 100).length,
      sseTotal: window.__sse.total,
      sseByType: window.__sse.byType,
      stats: document.querySelector('.foot__stats')?.innerText ?? '',
      rowStatusCounts: (() => {
        const out = {}
        for (const el of document.querySelectorAll('.panel__body.queue__body .item__status')) {
          const text = (el.innerText || '').trim()
          out[text] = (out[text] || 0) + 1
        }
        return out
      })(),
      heapAfter: performance.memory ? performance.memory.usedJSHeapSize : 0,
    }
  })

  Object.assign(result, measured)

  // 列表是虚拟化的：上面数到的只是当前挂着的行。再看一眼底部，
  // 确认远端那些行的状态也已经落地（不能只信页脚）。
  await page.evaluate(() => {
    const body = document.querySelector('.panel__body.queue__body')
    if (body) body.scrollTop = body.scrollHeight
  })
  await page.waitForTimeout(600)
  result.bottom = await page.evaluate(() => {
    const body = document.querySelector('.panel__body.queue__body')
    const items = [...document.querySelectorAll('.panel__body.queue__body .item')]
    const statuses = {}
    for (const el of items) {
      const text = (el.querySelector('.item__status')?.innerText || '').trim()
      statuses[text] = (statuses[text] || 0) + 1
    }
    return {
      scrollTop: Math.round(body?.scrollTop ?? 0),
      rendered: items.length,
      statuses,
      lastNames: items.slice(-3).map((el) => (el.querySelector('.item__name')?.innerText || '').trim()),
    }
  })
  return JSON.stringify(result, null, 1)
}
