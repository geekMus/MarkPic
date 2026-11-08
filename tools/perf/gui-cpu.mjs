// 探针：用 CDP CPU profile 给「1000 篇一轮」的浏览器侧开销做归因（自耗时 top 20）。
// 用 browser_run_code_unsafe 跑：脚本必须是 async (page) => {...}，且文件要放在 C:\Users\mus 下。
async (page) => {
  const dir = 'C:\\Users\\mus\\AppData\\Local\\Temp\\cmp-gui-1000'
  const paths = []
  for (let i = 0; i < 1000; i += 1) paths.push(dir + '\\doc-' + String(i).padStart(4, '0') + '.md')
  const text = paths.join('\n')

  await page.setViewportSize({ width: 1280, height: 800 })

  // 幂等：同一个 page 上重复跑探针时不要把 EventSource 套两层。
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
  })

  await page.goto('http://127.0.0.1:17982', { waitUntil: 'load' })
  await page.waitForSelector('.foot__stats', { timeout: 20000 })
  await page.getByRole('button', { name: '手动输入路径' }).first().click()
  await page.waitForSelector('textarea', { timeout: 20000 })
  await page.waitForTimeout(800)

  await page.fill('textarea', text)
  await page.keyboard.press('Enter')
  await page.waitForFunction(
    () => (document.querySelector('.foot__stats')?.innerText ?? '').includes('1000'),
    null,
    { timeout: 40000, polling: 50 },
  )
  await page.waitForTimeout(500)

  await page.evaluate(() => {
    window.__sse.total = 0
    window.__sse.byType = {}
  })

  const client = await page.context().newCDPSession(page)
  await client.send('Profiler.enable')
  await client.send('Profiler.setSamplingInterval', { interval: 200 })
  await client.send('Profiler.start')

  const t0 = Date.now()
  await page.getByRole('button', { name: /开始转换/ }).click()
  await page.waitForFunction(
    () => [...document.querySelectorAll('button')].some((b) => /取消/.test(b.innerText)),
    null,
    { timeout: 20000, polling: 10 },
  )
  await page.waitForFunction(
    () => ![...document.querySelectorAll('button')].some((b) => /取消/.test(b.innerText)),
    null,
    { timeout: 120000, polling: 20 },
  )
  const settleMs = Date.now() - t0
  await page.waitForTimeout(500)

  const { profile } = await client.send('Profiler.stop')

  const byId = new Map()
  for (const node of profile.nodes) byId.set(node.id, node)
  const selfUs = new Map()
  profile.samples.forEach((id, i) => {
    const delta = profile.timeDeltas[i] ?? 0
    selfUs.set(id, (selfUs.get(id) ?? 0) + delta)
  })
  const rows = []
  for (const [id, us] of selfUs) {
    const node = byId.get(id)
    if (!node) continue
    const cf = node.callFrame
    rows.push({
      fn: cf.functionName || '(anonymous)',
      url: (cf.url || '').split('/').pop() + ':' + (cf.lineNumber + 1),
      selfMs: Math.round(us / 100) / 10,
    })
  }
  rows.sort((a, b) => b.selfMs - a.selfMs)
  const merged = new Map()
  for (const r of rows) {
    const key = r.fn + ' @ ' + r.url
    merged.set(key, (merged.get(key) ?? 0) + r.selfMs)
  }
  const top = [...merged.entries()]
    .map(([key, selfMs]) => ({ where: key, selfMs: Math.round(selfMs * 10) / 10 }))
    .sort((a, b) => b.selfMs - a.selfMs)
    .slice(0, 20)

  const stats = await page.evaluate(() => ({
    sse: window.__sse,
    footer: document.querySelector('.foot__stats')?.innerText ?? '',
    heap: performance.memory ? performance.memory.usedJSHeapSize : 0,
  }))

  return JSON.stringify(
    {
      settleMs,
      profileMs: Math.round(profile.endTime - profile.startTime),
      sse: stats.sse,
      footer: stats.footer,
      heapMB: Math.round(stats.heap / 1048576),
      topSelfTime: top,
    },
    null,
    1,
  )
}
