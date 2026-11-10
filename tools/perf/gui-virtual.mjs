/**
 * 虚拟化列表的体检探针（第 18 轮）：确认「只挂可见行」没有把列表弄坏。
 *
 * 用前先：
 *   1) 启动打包版：MARKPIC_DATA_DIR=%TEMP%\cmp-gui-5000-data & bin\MarkPic.exe --port 17982 --no-open
 *   2) 让假图床在 36678 上跑：go run ./tools/mockpicgo -port 36678 -delay 5ms -quiet
 *   3) 把本文件复制到 C:\Users\mus\.playwright-mcp\ 再用 playwright 的 browser_run_code_unsafe 调
 *      （那个工具只允许 C:\Users\mus 与 C:\Users\mus\.playwright-mcp 两个根）
 *
 * 检查三件事：
 *   - 视口里不留空白带（虚拟化最容易出的错是滚动时露白）；
 *   - 同一个 scrollTop 落在同一批行上（展开一行、滚走、再滚回来，行号必须一致）；
 *   - 展开的行滚出窗口再回来时高度记帐不串（总高度回到展开前的值）。
 */
async (page) => {
  const dir = 'C:\\Users\\mus\\AppData\\Local\\Temp\\cmp-gui-5000';
  const count = 1000
  const paths = Array.from({ length: count }, (_, i) => `${dir}\\doc-${String(i).padStart(4, '0')}.md`).join('\n')

  await page.request.put('http://127.0.0.1:17982/api/settings', {
    data: { endpoint: 'http://127.0.0.1:36678', concurrency: 16 },
  })
  await page.goto('http://127.0.0.1:17982', { waitUntil: 'domcontentloaded' })
  await page.waitForSelector('.foot__stats', { timeout: 15000 })
  await page.getByRole('button', { name: '手动输入路径' }).first().click()
  await page.evaluate((text) => {
    const el = document.querySelector('textarea')
    el.value = text
    el.dispatchEvent(new Event('input', { bubbles: true }))
  }, paths)
  await page.press('textarea', 'Enter')
  await page.waitForFunction((n) => document.querySelector('.foot__stats')?.textContent?.includes(`${n} 个文件`), count, { timeout: 60000 })
  await page.keyboard.press('Control+Enter')
  await page.getByRole('button', { name: /取消/ }).waitFor({ timeout: 15000 }).catch(() => {})
  await page.getByRole('button', { name: /取消/ }).waitFor({ state: 'detached', timeout: 60000 }).catch(() => {})

  const state = () =>
    page.evaluate(() => {
      const b = document.querySelector('.queue__body')
      const bodyTop = b.getBoundingClientRect().top
      const rows = Array.from(document.querySelectorAll('.item'))
      const viewport = Math.round(b.getBoundingClientRect().height)
      const spans = rows
        .map((el) => {
          const r = el.getBoundingClientRect()
          return { top: Math.round(r.top - bodyTop), bottom: Math.round(r.bottom - bodyTop) }
        })
        .filter((s) => s.bottom > 0 && s.top < viewport)
        .sort((a, x) => a.top - x.top)
      let cursor = 0
      let gap = 0
      for (const s of spans) {
        if (s.top > cursor) gap = Math.max(gap, s.top - cursor)
        cursor = Math.max(cursor, s.bottom)
      }
      return {
        scrollTop: b.scrollTop,
        scrollHeight: b.scrollHeight,
        viewport,
        rowsInDom: rows.length,
        firstId: rows[0]?.dataset?.rowId,
        ids: rows.map((r) => r.dataset.rowId),
        details: document.querySelectorAll('.details').length,
        gapInViewport: gap,
      }
    })

  const setScroll = (px) => page.evaluate((v) => { document.querySelector('.queue__body').scrollTop = v }, px)

  const base = await state()
  await setScroll(25000)
  await page.waitForTimeout(400)
  const anchored = await state()

  // 展开视口第一行：行高会从 ~90px 变成几百 px
  await page.locator('.item__toggle').nth(0).click()
  await page.waitForTimeout(600)
  const expanded = await state()

  // 滚到很远，让那一行被卸载
  await setScroll(80000)
  await page.waitForTimeout(600)
  const faraway = await state()

  // 滚回来：行号必须与 anchored 完全一致，总高度必须回到展开前的值
  await setScroll(25000)
  await page.waitForTimeout(600)
  const back = await state()

  return {
    base,
    anchored,
    expanded,
    faraway,
    back,
    sameRowsAfterRoundTrip: JSON.stringify(anchored.ids) === JSON.stringify(back.ids),
    noBlankBands: [anchored, expanded, faraway, back].every((s) => s.gapInViewport === 0),
    heightRestored: back.scrollHeight === anchored.scrollHeight,
    expandedGrewBy: expanded.scrollHeight - anchored.scrollHeight,
  }
}
