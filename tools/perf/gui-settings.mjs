// 设置界面的「往返」体检：每个字段在界面上改一遍 → 保存 → 看后端收到什么 → 刷新页面还在不在。
//
// 用法：把本文件复制到 C:\Users\mus\.playwright-mcp\ 下再调用
//   browser_run_code_unsafe({ filename: "C:\\Users\\mus\\.playwright-mcp\\gui-settings.mjs" })
// 前提：应用已经跑起来（--no-open，端口写进 URL），并且页面语言是中文。
//
// 静态检查（SETTINGS_KEYS、settingsPatch）只能证明「字段名对得上」，
// 证明不了「界面上那个控件真的接到了这个字段、保存真的发出去了、刷新真的读回来了」——
// 这个探针跑的就是后者。第 4 轮就是这样抓到 reuseUploads 的开关存不下去。
async (page) => {
  const BASE = 'http://127.0.0.1:17990'
  const report = []
  const say = (s) => report.push(s)

  const apiSettings = async () =>
    await page.evaluate(async () => (await fetch('/api/settings')).json())

  const openSettings = async () => {
    await page.getByRole('button', { name: '打开设置' }).click()
    await page.waitForSelector('#settings-title', { state: 'visible' })
  }
  const closeSettings = async () => {
    const btn = page.getByRole('button', { name: '关闭设置' })
    if (await btn.count()) await btn.first().click()
    await page.waitForTimeout(200)
  }

  // 控件的定位：都用弹窗里的标签文字，避免依赖 class
  const panel = () => page.locator('[role="dialog"]:has(#settings-title)')
  const fieldIn = (label) => panel().locator(`label:has-text("${label}") input`).first()
  // 输出目录那一段用的是 div.field（里面还有一个「选择」按钮），不是 label
  const outDirField = () => panel().locator('div.field:has-text("输出目录") input').first()
  const check = (label) => panel().locator(`label.check:has-text("${label}") input[type=checkbox]`).first()
  const seg = (group, item) =>
    panel().getByRole('group', { name: group }).getByRole('button', { name: item })

  await page.goto(BASE)
  await page.waitForSelector('.foot__stats, .foot', { timeout: 10000 })
  await openSettings()

  // 1) 先读一遍界面上的初始值
  const before = {
    endpoint: await fieldIn('服务地址').inputValue(),
    concurrency: await fieldIn('并发数').inputValue(),
    retries: await fieldIn('失败重试').inputValue(),
    suffix: await fieldIn('副本后缀').inputValue(),
    backup: await check('覆盖前备份原文件').isChecked(),
    reuse: await check('复用上次的上传结果').isChecked(),
  }
  say(`界面初始值：${JSON.stringify(before)}`)
  const apiBefore = await apiSettings()
  say(`后端初始值：endpoint=${apiBefore.endpoint} concurrency=${apiBefore.concurrency} retries=${apiBefore.retries} output=${apiBefore.output} suffix=${apiBefore.suffix} backup=${apiBefore.backup} dryRun=${apiBefore.dryRun} reuseUploads=${apiBefore.reuseUploads}`)

  // 2) 每个字段都改成好认的值
  await fieldIn('服务地址').fill('127.0.0.1:36678')      // 故意不写协议，看后端会不会补
  await fieldIn('并发数').fill('12')
  await fieldIn('并发数').blur()
  await fieldIn('失败重试').fill('3')
  await fieldIn('失败重试').blur()
  await check('复用上次的上传结果').uncheck()

  // 输出方式走一遍三段：侧挂 → 原地 → 指定目录
  await seg('输出方式', '原地替换').click()
  await page.waitForTimeout(100)
  const suffixVisibleInPlace = await panel().locator('label:has-text("副本后缀")').count()
  const inPlaceWarning = await panel().locator('text=原地替换会直接改写原始文件').count()
  say(`切到「原地替换」：副本后缀输入框 ${suffixVisibleInPlace ? '还在（不该在）' : '已隐藏 ✓'}，风险提示 ${inPlaceWarning ? '出现 ✓' : '没出现（该出现）'}`)

  await seg('输出方式', '指定目录').click()
  await page.waitForTimeout(100)
  await outDirField().fill(String.raw`C:\Users\mus\AppData\Local\Temp\cmp-settings-out`)
  say('已切到「指定目录」并填了输出目录')

  await seg('输出方式', '旁挂副本').click()
  await page.waitForTimeout(100)
  await fieldIn('副本后缀').fill('.roundtrip')
  await check('覆盖前备份原文件').uncheck()
  await check('试运行').check()

  // 3) 保存并在网络层确认发出去的载荷
  const reqPromise = page.waitForRequest((r) => r.url().endsWith('/api/settings') && r.method() === 'PUT')
  await page.getByRole('button', { name: '保存设置' }).click()
  const req = await reqPromise
  const sent = JSON.parse(req.postData() ?? '{}')
  say(`界面 PUT 出去的载荷：${JSON.stringify(sent)}`)
  await page.waitForTimeout(400)

  // 4) 后端存下来的值
  const apiAfter = await apiSettings()
  const checks = [
    ['endpoint 补了协议', apiAfter.endpoint === 'http://127.0.0.1:36678'],
    ['concurrency=12', apiAfter.concurrency === 12],
    ['retries=3', apiAfter.retries === 3],
    ['output=sidecar', apiAfter.output === 'sidecar'],
    ['suffix=.roundtrip', apiAfter.suffix === '.roundtrip'],
    ['outDir 记住了', apiAfter.outDir.endsWith('cmp-settings-out')],
    ['backup=false', apiAfter.backup === false],
    ['dryRun=true', apiAfter.dryRun === true],
    ['reuseUploads=false', apiAfter.reuseUploads === false],
  ]
  for (const [name, ok] of checks) say(`后端 ${ok ? '✓' : '✗'} ${name}`)
  say(`后端实际值：${JSON.stringify(apiAfter)}`)

  // 5) 刷新页面，看界面是不是把这些值读回来了
  await closeSettings()
  await page.reload()
  await page.waitForSelector('.foot__stats, .foot', { timeout: 10000 })
  await openSettings()
  const after = {
    endpoint: await fieldIn('服务地址').inputValue(),
    concurrency: await fieldIn('并发数').inputValue(),
    retries: await fieldIn('失败重试').inputValue(),
    suffix: await fieldIn('副本后缀').inputValue(),
    backup: await check('覆盖前备份原文件').isChecked(),
    dryRun: await check('试运行').isChecked(),
    reuse: await check('复用上次的上传结果').isChecked(),
  }
  say(`刷新后界面显示：${JSON.stringify(after)}`)
  const uiOk = [
    ['服务地址', after.endpoint === 'http://127.0.0.1:36678'],
    ['并发数', after.concurrency === '12'],
    ['失败重试', after.retries === '3'],
    ['副本后缀', after.suffix === '.roundtrip'],
    ['备份', after.backup === false],
    ['试运行', after.dryRun === true],
    ['复用上传结果', after.reuse === false],
  ]
  for (const [name, ok] of uiOk) say(`界面 ${ok ? '✓' : '✗'} ${name}`)

  // 6) 改了不保存：dirty 标记与「还原」
  const dirtyBadge = () => panel().locator('text=未保存').count()
  say(`刚打开时「未保存」标记：${(await dirtyBadge()) ? '在（不该在）' : '没有 ✓'}`)
  await fieldIn('并发数').fill('7')
  await fieldIn('并发数').blur()
  await page.waitForTimeout(150)
  say(`改了并发数之后「未保存」标记：${(await dirtyBadge()) ? '出现 ✓' : '没出现（该出现）'}`)
  await panel().getByRole('button', { name: '还原' }).click()
  await page.waitForTimeout(200)
  const reverted = await fieldIn('并发数').inputValue()
  say(`点「还原」之后：并发数=${reverted}（应当是 12），「未保存」标记 ${(await dirtyBadge()) ? '还在（不该在）' : '消失 ✓'}`)

  // 改了直接关闭弹窗：不该落盘
  await fieldIn('并发数').fill('9')
  await fieldIn('并发数').blur()
  await page.waitForTimeout(150)
  await closeSettings()
  const apiFinal = await apiSettings()
  say(`改了并发数却直接关掉弹窗：后端仍然是 ${apiFinal.concurrency}（应当是 12）${apiFinal.concurrency === 12 ? ' ✓' : ' ✗'}`)

  await closeSettings()
  return report.join('\n')
}
