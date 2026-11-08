async (page) => {
  // 一篇 5 MB / 1250 张图引用的文档：预览、入队、展开明细，看展开会不会把界面拖死。
  // 用法：先 Copy-Item 到 C:\Users\mus\.playwright-mcp\ 再 browser_run_code_unsafe。
  const doc = 'C:\\Users\\mus\\AppData\\Local\\Temp\\cmp-huge\\huge.md'

  await page.setViewportSize({ width: 1280, height: 800 })

  await page.addInitScript(() => {
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
  await page.waitForTimeout(500)

  const result = {}

  await page.evaluate(() => {
    window.__frames.length = 0
  })
  const tPreview0 = Date.now()
  await page.evaluate((value) => {
    const el = document.querySelector('textarea')
    el.value = value
    el.dispatchEvent(new Event('input', { bubbles: true }))
  }, doc)
  await page.keyboard.press('Enter')
  await page.waitForFunction(
    () => document.querySelectorAll('.panel__body.queue__body .item').length >= 1,
    null,
    { timeout: 60000, polling: 20 },
  )
  result.previewMs = Date.now() - tPreview0
  await page.waitForTimeout(800)
  result.framesAfterPreview = await page.evaluate(() => {
    const gaps = []
    for (let i = 1; i < window.__frames.length; i += 1) gaps.push(window.__frames[i] - window.__frames[i - 1])
    gaps.sort((a, b) => b - a)
    return { frames: window.__frames.length, worst: gaps.slice(0, 4).map((g) => Math.round(g)) }
  })
  result.statsAfterPreview = await page.evaluate(
    () => document.querySelector('.foot__stats')?.innerText ?? '',
  )

  // 展开这一行：明细列表有多少项、展开要多久、掉多少帧
  await page.evaluate(() => {
    window.__frames.length = 0
  })
  const tExpand0 = Date.now()
  await page.evaluate(() => {
    const toggle = document.querySelector('.panel__body.queue__body .item .item__toggle')
    if (toggle) toggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  })
  await page.waitForTimeout(4000)
  result.expandMs = Date.now() - tExpand0
  result.expanded = await page.evaluate(
    () => document.querySelector('.panel__body.queue__body .item .item__toggle')?.getAttribute('aria-expanded') ?? '',
  )
  result.framesAfterExpand = await page.evaluate(() => {
    const gaps = []
    for (let i = 1; i < window.__frames.length; i += 1) gaps.push(window.__frames[i] - window.__frames[i - 1])
    gaps.sort((a, b) => b - a)
    return {
      frames: window.__frames.length,
      worst: gaps.slice(0, 6).map((g) => Math.round(g)),
      over100: gaps.filter((g) => g > 100).length,
    }
  })
  result.detail = await page.evaluate(() => {
    const row = document.querySelector('.panel__body.queue__body .item')
    const lines = row ? row.querySelectorAll('.item__img, .item__detail, .item__row').length : 0
    return {
      domNodesInRow: row ? row.querySelectorAll('*').length : 0,
      detailLines: lines,
      rowHeight: row ? Math.round(row.getBoundingClientRect().height) : 0,
      bodyScrollHeight: document.querySelector('.panel__body.queue__body')?.scrollHeight ?? 0,
    }
  })

  // 展开状态滚到底：下面那些明细行是不是也在 DOM 里
  await page.evaluate(() => {
    const body = document.querySelector('.panel__body.queue__body')
    if (body) body.scrollTop = body.scrollHeight
  })
  await page.waitForTimeout(800)
  result.bottom = await page.evaluate(() => {
    const rows = [...document.querySelectorAll('.panel__body.queue__body .item')]
    return {
      rowsInDom: rows.length,
      scrollTop: Math.round(document.querySelector('.panel__body.queue__body')?.scrollTop ?? 0),
      lastDetailText: rows.length
        ? (rows[rows.length - 1].innerText || '').split('\n').slice(-2).join(' | ').slice(0, 120)
        : '',
    }
  })

  return JSON.stringify(result, null, 1)
}
