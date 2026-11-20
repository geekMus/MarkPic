/**
 * 扫描分两档，界面上的两类卡片正好对应：
 *
 *   1. **发现**（文件夹 / 盘）：只走目录、数 Markdown，不解析内容 —— 卡片上写
 *      「已找到 N 份」，用户点「确认导入」之后才把文件铺成卡片。
 *   2. **统计**（文件卡片）：逐份读盘解析、数图片。确认导入时一次提交，后端按
 *      机器能力限流（路径并发 + 文档并发两层），不会真的同时开一万个扫描。
 *
 * 所以「文件夹里有几份文档」和「每份里有几张图」是两件事，分别由这两档回答。
 */
import { markRaw } from 'vue'
import { cancelScan as requestCancelScan, describeError, scanStart as requestScanStart } from '../../api'
import type { FileResult, RemoteMode, ScanEvent } from '../../types'
import { t } from '../../i18n'
import { activePage, pageLocalId, pages, pageRows, rows } from './page'
import { pathKey, splitPath } from './paths'
import { reindexPage, seedImagesFromRows } from './rows'
import type { RunRow, ScanState } from './types'

/* ---------------- 扫描 ---------------- */

export interface ScanBatch {
  id: string
  mode: RemoteMode
  paths: string[]
  timer: number | null
  /** true = 只发现文件；false = 逐份解析统计。 */
  discover: boolean
  /** 请求路径 → 发现的文档（发现档）或扫描结果（统计档）。 */
  found: Map<string, FileResult[]>
  seen: Set<string>
}

export const scanBatches = new Map<string, ScanBatch>()
export let scanSeq = 0

/** 多久没有任何事件就当这一批已经没了（按「静默」算，不按总时长）。 */
export const SCAN_STALL_MS = 30_000

function touchBatch(batch: ScanBatch, state: ScanState, message?: string): void {
  if (batch.timer !== null) window.clearTimeout(batch.timer)
  batch.timer = window.setTimeout(() => {
    batch.timer = null
    finishBatch(batch, state, message)
  }, SCAN_STALL_MS)
}

export function rowOfPage(mode: RemoteMode, path: string): RunRow | undefined {
  const key = pathKey(path)
  return pageRows(mode).find((row) => pathKey(row.path) === key)
}

/** 把一份文档的结果落到它自己那一行（拖进来的就是这份文件 / 统计完成）。 */
function applyFileToRow(row: RunRow, file: FileResult): void {
  row.result = markRaw(file)
  row.images = file.images
  row.total = file.images
  row.missing = false
  row.scan = file.status === 'failed' ? 'failed' : 'ok'
  row.scanError = file.status === 'failed' ? file.error : undefined
  row.scanBatch = undefined
}

/** 发现档：把「这是一份文档」记到行上，图片数留空（还没数）。 */
function markAsFileCard(row: RunRow, file: FileResult): void {
  row.path = file.path
  row.name = file.name
  row.dir = file.dir
  row.scanCard = false
  row.images = null
  row.result = null
  row.scan = 'idle'
  row.scanBatch = undefined
}

/**
 * 把一批文档变成队列行。
 *
 * pendingCounts=true 用于「确认导入」：卡片先摆出来，图片数等统计回来再填；
 * false 用于统计完成后的展开（带着结果一起摆）。
 */
function buildRows(mode: RemoteMode, files: readonly FileResult[], pendingCounts: boolean): RunRow[] {
  const page = pages[mode]
  const taken = new Set<string>()
  const built: RunRow[] = []
  for (const file of files) {
    const key = pathKey(file.path)
    if (taken.has(key) || page.pathIndex.has(key)) continue
    taken.add(key)
    const fallback = splitPath(file.path)
    built.push({
      id: pageLocalId(mode),
      path: file.path,
      name: file.name || fallback.name,
      dir: file.dir || fallback.dir,
      images: pendingCounts ? null : file.images,
      phase: 'queued',
      uploaded: 0,
      total: pendingCounts ? 0 : file.images,
      result: pendingCounts ? null : markRaw(file),
      scan: pendingCounts ? 'idle' : file.status === 'failed' ? 'failed' : 'ok',
      scanError: pendingCounts || file.status !== 'failed' ? undefined : file.error,
    })
  }
  return built
}

/**
 * 收掉一批。
 *
 * 发现档：目录行留成一张「扫描卡片」，把发现结果挂在行上，等用户确认导入；
 * 统计档：文件行各自拿到结果（单份直接落，整批出现时见 expandBatch）。
 */
export function finishBatch(batch: ScanBatch, state: ScanState, message?: string): void {
  if (batch.timer !== null) {
    window.clearTimeout(batch.timer)
    batch.timer = null
  }
  scanBatches.delete(batch.id)

  const page = pages[batch.mode]
  let changed = false
  for (const path of batch.paths) {
    const row = rowOfPage(batch.mode, path)
    if (!row) continue
    if (row.scanBatch !== undefined && row.scanBatch !== batch.id) continue

    // 发现档：留卡片、挂结果。
    if (batch.discover) {
      const found = batch.found.get(pathKey(path)) ?? []
      row.scanBatch = undefined
      row.scanCard = true
      row.scanFiles = found
      row.scanFound = found.length
      if (found.length === 0) {
        row.scan = state === 'cancelled' ? 'cancelled' : 'failed'
        row.scanError = state === 'cancelled' ? undefined : (message ?? t('scan.notFound'))
        row.missing = state !== 'cancelled'
      } else {
        row.scan = state === 'cancelled' ? 'cancelled' : 'ok'
        row.scanError = undefined
        row.missing = false
      }
      changed = true
      continue
    }

    // 统计档：这一份是文件本身，结果早就落到行上了。
    if (row.result) {
      row.scan = 'ok'
      row.scanBatch = undefined
      continue
    }

    const found = batch.found.get(pathKey(path)) ?? []
    if (found.length === 0) {
      row.scan = state === 'cancelled' ? 'cancelled' : 'failed'
      row.scanError = state === 'cancelled' ? undefined : (message ?? t('scan.notFound'))
      row.scanBatch = undefined
      row.missing = state !== 'cancelled'
      continue
    }
    const index = page.rows.indexOf(row)
    if (index < 0) continue
    page.rows.splice(index, 1, ...buildRows(batch.mode, found, false))
    changed = true
  }
  if (changed) reindexPage(page)
  if (batch.mode === activePage) seedImagesFromRows()
}

/**
 * 开扫。
 *
 * discover=true（文件夹/盘）：只发现 Markdown；discover=false（文件）：数图片。
 */
export async function scanPaths(mode: RemoteMode, paths: readonly string[], discover = false): Promise<void> {
  const list: string[] = []
  const seen = new Set<string>()
  for (const raw of paths) {
    const path = raw.trim()
    if (path === '' || seen.has(pathKey(path))) continue
    seen.add(pathKey(path))
    list.push(path)
  }
  if (list.length === 0) return

  const id = 'scan-' + Date.now().toString(36) + '-' + ++scanSeq
  const batch: ScanBatch = { id, mode, paths: list, timer: null, discover, found: new Map(), seen: new Set() }
  scanBatches.set(id, batch)

  for (const path of list) {
    const row = rowOfPage(mode, path)
    if (!row) continue
    row.scan = 'scanning'
    row.scanError = undefined
    row.scanBatch = batch.id
    row.scanFound = 0
    row.scanDirs = 0
  }

  touchBatch(batch, 'failed', t('scan.timeout'))

  try {
    await requestScanStart({ scanId: id, paths: list, discover })
  } catch (err) {
    finishBatch(batch, 'failed', describeError(err, t('scan.failed')))
  }
}

/** scan.progress / scan.file / scan.done 落到对应的卡片上。 */
export function onScanEvent(event: ScanEvent): void {
  const batch = scanBatches.get(event.runId)
  if (!batch) return

  if (event.type === 'scan.done') {
    finishBatch(batch, event.cancelled ? 'cancelled' : 'ok')
    return
  }

  touchBatch(batch, 'failed', t('scan.timeout'))

  const host = rowOfPage(batch.mode, event.path ?? '')
  if (!host) return
  if (host.scanBatch !== undefined && host.scanBatch !== batch.id) return

  if (event.type === 'scan.progress') {
    host.scanFound = event.found ?? 0
    host.scanDirs = event.dirs ?? 0
    if (event.current !== undefined) host.scanCurrent = event.current
    if (event.file !== undefined) host.scanFile = event.file
    return
  }

  const files = event.scanned ?? []
  if (files.length === 0) {
    host.scan = 'failed'
    host.scanError = t('scan.notFound')
    host.scanBatch = undefined
    host.missing = true
    return
  }

  // 拖进来的就是一份文件：直接变成文件卡片，并立即统计它的图片数。
  if (files.length === 1 && pathKey(files[0].path) === pathKey(host.path)) {
    markAsFileCard(host, files[0])
    if (batch.discover) {
      void scanPaths(batch.mode, [files[0].path], false)
    } else {
      applyFileToRow(host, files[0])
      if (batch.mode === activePage) seedImagesFromRows()
    }
    return
  }

  // 目录：先把发现结果攒着，收尾时挂到卡片上（等用户确认导入）。
  const key = pathKey(host.path)
  const bucket = batch.found.get(key) ?? []
  for (const file of files) {
    const fileKey = pathKey(file.path)
    if (batch.seen.has(fileKey)) continue
    batch.seen.add(fileKey)
    bucket.push(file)
  }
  batch.found.set(key, bucket)
  if (batch.discover) host.scanFound = bucket.length
}

/**
 * 确认导入：把扫描卡片换成逐个文件卡片，并开始统计每份的图片数。
 *
 * 文件是一次性全提交给后端的：后端的路径池与文档槽按机器能力限流，前端不必
 * 自己排「同时开几个扫描」—— 一次开一万个是不可能的，那由后端的两层闸门负责。
 */
export function confirmImport(mode: RemoteMode, rowId: string): number {
  const page = pages[mode]
  const row = page.rows.find((item) => item.id === rowId)
  if (!row || !row.scanCard) return 0
  const files = row.scanFiles ?? []
  if (files.length === 0) return 0

  const index = page.rows.indexOf(row)
  if (index < 0) return 0
  const built = buildRows(mode, files, true)
  page.rows.splice(index, 1, ...built)
  reindexPage(page)
  if (mode === activePage) seedImagesFromRows()

  void scanPaths(mode, built.map((item) => item.path), false)
  return built.length
}

/** 掐掉一批（整批或单个文件）。 */
export function requestBatchCancel(batch: ScanBatch, path?: string): void {
  void requestCancelScan(path === undefined ? { scanId: batch.id } : { scanId: batch.id, path }).catch(() => {
    // 取消是「尽力而为」：后端已经不认这一批了也算成功，界面上的状态早就改好了。
  })
}

/** 取消某一张卡片的扫描（确认框之后调用）。 */
export function cancelRowScan(id: string): void {
  const row = rows.value.find((item) => item.id === id)
  if (!row) return
  if ((row.scan ?? 'idle') !== 'scanning') return

  const key = pathKey(row.path)
  for (const batch of scanBatches.values()) {
    if (batch.mode !== activePage) continue
    if (!batch.paths.some((path) => pathKey(path) === key)) continue
    requestBatchCancel(batch, row.path)
    if (!row.result && (row.scanFound ?? 0) === 0) {
      row.scan = 'cancelled'
      row.scanBatch = undefined
      row.images = null
    }
    return
  }

  row.scan = 'cancelled'
  row.scanError = undefined
  row.scanBatch = undefined
  row.images = null
}

/** 重新扫一张卡片：扫描卡片重跑发现，文件卡片重跑统计。 */
export function retryRowScan(id: string): void {
  const row = rows.value.find((item) => item.id === id)
  if (!row) return
  if ((row.scan ?? 'idle') === 'scanning') return
  void scanPaths(activePage, [row.path], row.scanCard === true)
}

/** 掐掉这一页所有还在跑的扫描（清空队列、离开这一页时兜底）。 */
export function cancelPageScans(): void {
  for (const batch of [...scanBatches.values()]) {
    if (batch.mode !== activePage) continue
    if (batch.timer !== null) {
      window.clearTimeout(batch.timer)
      batch.timer = null
    }
    scanBatches.delete(batch.id)
    requestBatchCancel(batch)
  }
}
