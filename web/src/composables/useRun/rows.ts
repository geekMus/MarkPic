/**
 * 队列行本身：怎么建、怎么按 id 找到、结果怎么落到行上。
 *
 * 一行就是「一个文件」在这轮里的全部账面：张数、进度、明细、结局。事件是按 id 找行、
 * 按增量改行，绝不整表刷新 —— 1000 行的时候整表重建会把界面卡住。
 */
import { markRaw, shallowReactive } from 'vue'
import type { FileResult, RemoteMode, RunFileInfo } from '../../types'
import { t } from '../../i18n'
import { activePage, eventIndex, lastReveal, live, pages, pathIndex, revealIndex, rowIndex, rows } from './page'
import type { QueuePage } from './page'
import { emptyCounters } from './types'
import { pathKey } from './paths'
import type { RunRow } from './types'

export function liveReset(): void {
  Object.assign(live, emptyCounters())
  lastReveal.value = null
  revealIndex.value = -1
}

/** 队列刚建好时，用每行预估的图片数垫上总数，别让页脚在跑之前显示「0 张图」。 */
export function seedImagesFromRows(): void {
  let images = 0
  for (const row of rows.value) images += row.images ?? row.total
  live.images = images
}

/** 远程地址的判定跟 Go 侧 isRemoteURL 一致：http/https/协议相对。 */
export function isRemoteTarget(target: string): boolean {
  const t = target.trim().toLowerCase()
  // ftp:// 与 data: 这类我们碰不了，Go 不会把它们列成单元，这里也不能算进分母。
  return t.startsWith('http://') || t.startsWith('https://') || t.startsWith('//')
}

/**
 * 从一份明细里数这一轮真正要处理的单元。
 *
 * 上传按本地绝对路径去重（同一张图引用多次只传一次）；检测/下载按远程地址去重
 * （同一个地址只探一次）。和 Go 侧 runUnits 的口径一致，所以预览估出来的总数
 * 能直接当分母，第一个 file.progress 到了之后再换成服务端的权威值。
 */
export function workUnitsIn(result: FileResult | null | undefined, mode: RemoteMode): number {
  const items = result?.items
  if (!items || items.length === 0) return 0
  const seen = new Set<string>()
  for (const item of items) {
    if (mode === 'ignore') {
      // 本地图只有真的存在才会进 groups；absolutePath 为空的是缺失引用，不算单元。
      if (item.absolutePath === '') continue
      seen.add(item.absolutePath.toLowerCase())
      continue
    }
    if (!isRemoteTarget(item.target)) continue
    seen.add(item.target.trim())
  }
  return seen.size
}

export function rowAt(id: string): RunRow | null {
  const index = eventIndex.get(id) ?? rowIndex.get(id)
  if (index === undefined) return null
  return rows.value[index] ?? null
}

/** 重建某一页的三张索引表。目录展开时别的页也可能在动，所以要能按页重建。 */
export function reindexPage(page: QueuePage): void {
  page.rowIndex.clear()
  page.eventIndex.clear()
  page.pathIndex.clear()
  page.rows.forEach((row, index) => {
    page.rowIndex.set(row.id, index)
    if (row.serverId) page.eventIndex.set(row.serverId, index)
    page.pathIndex.set(pathKey(row.path), index)
  })
}

export function reindexRows(): void {
  // rows.value 可能刚被整体替换过（applyStarted 会重建数组）：先把这一页的引用同步过来，
  // 再按它建索引。不同步的话索引表记的是旧数组的顺序，之后按路径 / 服务端 id 找行
  // 就会找错 —— 表现是「卡片标题和展开的明细对不上」。
  const page = pages[activePage]
  page.rows = rows.value
  reindexPage(page)
}

export function pushRow(row: RunRow): void {
  rowIndex.set(row.id, rows.value.length)
  if (row.serverId) eventIndex.set(row.serverId, rows.value.length)
  pathIndex.set(pathKey(row.path), rows.value.length)
  rows.value.push(row)
}

/**
 * 找到 file.finished 该落到哪一行。
 *
 * 先按服务端 id 找；找不到再按路径找（事件合并、漏收 run.started 时的兜底）。
 */
export function rowForResult(id: string, path: string): RunRow | null {
  const byId = rowAt(id)
  if (byId) return byId
  const index = pathIndex.get(pathKey(path))
  if (index === undefined) return null
  return rows.value[index] ?? null
}

export function updateRow(
  id: string,
  patch: Partial<Pick<RunRow, 'phase' | 'uploaded' | 'total' | 'images'>>,
): void {
  const row = rowAt(id)
  if (!row) return
  Object.assign(row, patch)
}

export function rowFromResult(result: FileResult): RunRow {
  return {
    id: result.id,
    path: result.path,
    name: result.name,
    dir: result.dir,
    images: result.images,
    phase: 'queued',
    uploaded: 0,
    total: result.images,
    result: null,
    // 服务端能给出一份结果，说明这一份早就读通了 —— 它不该再挡住「开始」。
    scan: 'ok',
  }
}

/**
 * 把一份文件结果记到行上，并把增量写进 live 统计。
 *
 * 「图片总数」这一项要把该行原先的贡献换掉（排队时是预估、预检时是预检结果、
 * 完成后是真实结果），所以先减旧值再加新值。
 */
export function absorbResult(row: RunRow, result: FileResult): void {
  // 统计数字由后端随事件带回（event.summary），这里只管把结果落到行上。
  // 这一篇的活干完了：用结果里的明细重新数一遍单元。第一个 file.progress 之前
  // 的分母是按预览估算的，比真实值大的话会一直把总进度压低；一个单元都没有
  // （纯本地图/纯远程图）就该从分母里消失，不能把进度条永远钉在中间。
  row.workTotal = workUnitsIn(result, activePage)
  row.workDone = row.workTotal

  const index = rowIndex.get(row.id) ?? (row.serverId ? eventIndex.get(row.serverId) : undefined) ?? -1
  row.result = markRaw(result)
  row.liveItems = undefined
  row.phase = 'settled'
  row.uploaded = result.uploaded
  row.total = result.images
  row.images = result.images

  if (
    index > revealIndex.value &&
    (result.status === 'ok' || result.status === 'partial') &&
    result.outputPath
  ) {
    revealIndex.value = index
    lastReveal.value = result.outputPath
  }
}

export function applyStarted(files: RunFileInfo[]): void {
  // 后端只会回传真正找到的文件。用户加进来、但一个文件都没匹配上的路径要留在队列里，
  // 否则一点「开始转换」它就凭空消失，用户无从知道这条路径被忽略了。
  //
  // 匹配上的行按路径复用同一个对象（只改字段），组件 key（row.id）因此保持不变：
  // 1000 行不会因为「本地 id 换成服务端 id」而整批卸载重挂，展开的明细也不会被收起。
  const byPath = new Map<string, RunRow>()
  for (const row of rows.value) {
    const key = pathKey(row.path)
    if (!byPath.has(key)) byPath.set(key, row)
  }

  const known = new Set(files.map(file => pathKey(file.path)))
  const used = new Set<RunRow>()
  const started: RunRow[] = files.map(file => {
    const existing = byPath.get(pathKey(file.path))
    if (existing && !used.has(existing)) {
      used.add(existing)
      existing.serverId = file.id
      existing.path = file.path
      existing.name = file.name
      existing.dir = file.dir
      existing.images = file.images
      existing.phase = 'queued'
      existing.uploaded = 0
      existing.total = file.images
      existing.missing = false
      // 上一轮的明细先留着：这一轮每跑完一个文件，事件会把结果换掉。
      // 立刻置空会让检测页/下载页在跑的过程中变成空白页（看着像崩了）。
      existing.scan = 'ok'
      existing.scanError = undefined
      existing.scanBatch = undefined
      existing.liveItems = undefined
      return existing
    }
    return {
      id: file.id,
      serverId: file.id,
      path: file.path,
      name: file.name,
      dir: file.dir,
      images: file.images,
      phase: 'queued',
      uploaded: 0,
      total: file.images,
      result: null,
      scan: 'ok',
    }
  })
  const kept = rows.value
    .filter(row => row.missing && !known.has(pathKey(row.path)))
    .map(row => ({
      ...row,
      serverId: undefined,
      phase: 'queued' as const,
      uploaded: 0,
      total: row.images ?? 0,
      result: null,
      liveItems: undefined,
    }))

  rows.value = [...started, ...kept]
  // 服务端的 run.started 只带「这一篇几张图」，没带本模式的去重单元数；
  // 先用预览明细估一份，file.progress 一来就会被权威值覆盖。
  for (const row of rows.value) {
    row.workTotal = workUnitsIn(row.result, activePage)
    row.workDone = 0
  }
  reindexRows()
  liveReset()
  seedImagesFromRows()
}

/** 取消的那句提示按页说：站在「检测」上却说「转换已取消」很出戏。 */
export function cancelText(mode: RemoteMode): string {
  if (mode === 'check') return t('toast.runCancelled.check')
  if (mode === 'download') return t('toast.runCancelled.download')
  return t('toast.runCancelled')
}

export function markUnsettledCancelled(): void {
  for (const row of rows.value) {
    if (row.phase !== 'settled') row.phase = 'cancelled'
  }
}

/**
 * 给一行准备「运行中的逐条明细」。
 *
 * 预览时已经把整份 items 摆出来了，先照抄一份；第一张图处理完之后，Go 侧每条进度
 * 只说「第几处变成了什么」，这里按位置盖上去。照抄的一定要是浅响应数组：整份深响应
 * 在几万条明细的文档上会把界面拖住。
 */
export function seedLiveItems(row: RunRow): void {
  if (!row.liveItems) row.liveItems = shallowReactive([...(row.result?.items ?? [])])
}