async (page) => {
  const dir = 'C:\\Users\\mus\\AppData\\Local\\Temp\\cmp-gui-1000'
  const paths = []
  for (let i = 0; i < 1000; i += 1) paths.push(dir + '\\doc-' + String(i).padStart(4, '0') + '.md')
  const text = paths.join('\n')

  await page.setViewportSize({ width: 1280, height: 800 })

  await page.addInitScript(() => {
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

  const result = {}
  result.heapBefore = await page.evaluate(() =>
    performance.memory ? performance.memory.usedJSHeapSize : 0,
  )

  const tQueue0 = Date.now()
  await page.fill('textarea', text)
  await page.keyboard.press('Enter')
  await page.waitForFunction(
    () => (document.querySelector('.foot__stats')?.innerText ?? '').includes('1000'),
    null,
    { timeout: 40000, polling: 50 },
  )
  result.queueMs = Date.now() - tQueue0
  await page.waitForTimeout(400)
  result.statsAfterPreview = await page.evaluate(
    () => document.querySelector('.foot__stats')?.innerText ?? '',
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
      { timeout: 20000, polling: 10 },
    )
    sawCancel = true
  } catch (err) {
    sawCancel = false
  }
  result.sawCancel = sawCancel
  await page.waitForFunction(
    () => ![...document.querySelectorAll('button')].some((b) => /取消/.test(b.innerText)),
    null,
    { timeout: 120000, polling: 20 },
  )
  result.settleMs = Date.now() - t0
  await page.waitForTimeout(800)

  const measured = await page.evaluate(() => {
    const frames = window.__frames
    const gaps = []
    for (let i = 1; i < frames.length; i += 1) gaps.push(frames[i] - frames[i - 1])
    gaps.sort((a, b) => b - a)
    return {
      frames: frames.length,
      windowMs: frames.length > 1 ? Math.round(frames[frames.length - 1] - frames[0]) : 0,
      worstGaps: gaps.slice(0, 6).map((g) => Math.round(g)),
      over50: gaps.filter((g) => g > 50).length,
      over100: gaps.filter((g) => g > 100).length,
      sseTotal: window.__sse.total,
      sseByType: window.__sse.byType,
      stats: document.querySelector('.foot__stats')?.innerText ?? '',
      heapAfter: performance.memory ? performance.memory.usedJSHeapSize : 0,
    }
  })

  Object.assign(result, measured)
  return JSON.stringify(result, null, 1)
}
