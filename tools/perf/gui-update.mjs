/**
 * 更新流程在界面上的观感（第 21 轮）。
 *
 * 跑之前要先把演练环境搭起来（这份脚本不负责搭环境）：
 *   1. 用 tools/ 之外的静态服务器托管 version.json + candidate.exe，例如
 *      `%TEMP%\cmp-fileserver.exe <目录> 127.0.0.1:36681`；
 *   2. candidate.exe 用 `-ldflags "-X …/internal/version.Version=9.9.9-test"` 编，
 *      清单里的 version / sha256 / size 要和它一致；
 *   3. 把当前 exe 复制到别处，带 CMP_UPDATE_MANIFEST 指向清单、指定端口跑起来，
 *      例如 `CMP_UPDATE_MANIFEST=http://127.0.0.1:36681/version.json exe --port 17998 --no-open`。
 *
 * 脚本做的事：打开「关于」→ 点「立即更新」→ 看状态怎么走 →
 * 盯着页面会不会自己刷新（window 上放个标记，刷新后就没了）→ 读刷新后的页脚版本号。
 * 调用方式同其它探针：复制到 Playwright 允许的目录，再 browser_run_code_unsafe(filename)。
 */
async (page) => {
  const base = 'http://127.0.0.1:17998'
  await page.goto(base, { waitUntil: 'domcontentloaded' })
  await page.waitForSelector('.foot__ver', { timeout: 15000 })

  const before = (await page.locator('.foot__ver').innerText()).trim()
  await page.evaluate(() => {
    window.__probeMarker = 'alive'
  })

  await page.locator('.foot__ver').first().click()
  await page.waitForSelector('[role=dialog]', { timeout: 8000 })
  const dialog = page.locator('[role=dialog]').first()
  await page.waitForTimeout(1500)

  const statusOf = async () => {
    const txt = await dialog.innerText().catch(() => null)
    if (txt === null) return '对话框没了'
    const lines = txt.split('\n').map((s) => s.trim()).filter(Boolean)
    const i = lines.indexOf('状态')
    return i >= 0 ? lines[i + 1] : lines.slice(0, 3).join(' | ')
  }

  const seen = []
  await dialog.getByRole('button', { name: '立即更新', exact: true }).click().catch(() => {})

  const t0 = Date.now()
  while (Date.now() - t0 < 20000) {
    const line = await statusOf()
    if (seen[seen.length - 1] !== line) seen.push(line)
    if (line === '对话框没了' || line.includes('重启')) break
    await page.waitForTimeout(200)
  }

  let reloaded = false
  let reloadWaitMs = 0
  const t1 = Date.now()
  while (Date.now() - t1 < 60000) {
    let alive = null
    try {
      alive = await page.evaluate(() => window.__probeMarker ?? null)
    } catch {
      alive = null // 正在导航，执行上下文被销毁
    }
    if (alive !== 'alive') {
      reloaded = true
      reloadWaitMs = Date.now() - t1
      break
    }
    await page.waitForTimeout(300)
  }

  let after = 'unknown'
  try {
    await page.waitForSelector('.foot__ver', { timeout: 15000 })
    after = (await page.locator('.foot__ver').innerText()).trim()
  } catch {
    after = '找不到页脚版本号'
  }

  return { before, seen: seen.slice(0, 8), reloaded, reloadWaitMs, after }
}
