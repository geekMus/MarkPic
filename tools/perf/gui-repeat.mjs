async (page) => {
  // 连续跑同一批文档，看界面（JS 堆 / DOM 节点 / 行数 / SSE 计数）会不会越跑越大。
  // 用法：先 Copy-Item 到 C:\Users\mus\.playwright-mcp\ 再 browser_run_code_unsafe。
  const dir = 'C:\\Users\\mus\\AppData\\Local\\Temp\\cmp-gui-1000'
  const count = 1000
  const rounds = 8
  const paths = []
  for (let i = 0; i < count; i += 1) paths.push(dir + '\\doc-' + String(i).padStart(4, '0') + '.md')
  const text = paths.join('\n')

  await page.setViewportSize({ width: 1280, height: 800 })

  await page.addInitScript(() => {
    if (window.__sseHooked) return
    window.__sseHooked = true
    const Orig = window.EventSource
    const sse = { total: 0, byType: {}, instances: 0 }
    window.__sse = sse
    function CountingES(...args) {
      const es = new Orig(...args)
      sse.instances += 1
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
  })

  await page.goto('http://127.0.0.1:17982', { waitUntil: 'load' })
  await page.waitForSelector('.foot__stats', { timeout: 20000 })
  await page.getByRole('button', { name: '手动输入路径' }).first().click()
  await page.waitForSelector('textarea', { timeout: 20000 })
  await page.waitForTimeout(500)
  await page.evaluate((value) => {
    const el = document.querySelector('textarea')
    el.value = value
    el.dispatchEvent(new Event('input', { bubbles: true }))
  }, text)
  await page.keyboard.press('Enter')
  await page.waitForFunction(
    (n) => (document.querySelector('.foot__stats')?.innerText ?? '').includes(String(n)),
    count,
    { timeout: 40000, polling: 50 },
  )

  const snapshot = () =>
    page.evaluate(() => ({
      heapMB: performance.memory ? Math.round((performance.memory.usedJSHeapSize / 1048576) * 10) / 10 : 0,
      nodes: document.querySelectorAll('*').length,
      rowsInDom: document.querySelectorAll('.panel__body.queue__body .item').length,
      sseInstances: window.__sse.instances,
      stats: document.querySelector('.foot__stats')?.innerText.replace(/\s+/g, ' ') ?? '',
    }))

  const runs = []
  for (let i = 0; i < rounds; i += 1) {
    const t0 = Date.now()
    await page.getByRole('button', { name: /开始转换/ }).click()
    await page.waitForFunction(
      () => [...document.querySelectorAll('button')].some((b) => /取消/.test(b.innerText)),
      null,
      { timeout: 30000, polling: 10 },
    )
    await page.waitForFunction(
      () => ![...document.querySelectorAll('button')].some((b) => /取消/.test(b.innerText)),
      null,
      { timeout: 180000, polling: 20 },
    )
    const settleMs = Date.now() - t0
    await page.waitForTimeout(500)
    const snap = await snapshot()
    runs.push({ round: i + 1, settleMs, ...snap })
  }

  // 跑完把队列清掉，看内存能不能还回去（能还回去说明没有硬泄漏）
  await page.getByRole('button', { name: /清空队列/ }).first().click().catch(() => {})
  await page.waitForTimeout(1500)
  const afterClear = await snapshot()

  return JSON.stringify({ runs, afterClear }, null, 1)
}
