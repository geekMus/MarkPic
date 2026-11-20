/**
 * 每张卡片自己的扫描：发请求、按 scanId 收事件、结批。
 *
 * Go 侧是流式的：边遍历边报，发现一份推一条 scan.file，每 120ms 推一条 scan.progress
 * （已经找到多少份、走过多少目录）。界面这边刻意**不**边扫边铺卡片：
 *   - 拖进来的目录行在扫描期间就是一张卡片，显示「已找到 N 份」，点开能看到扫描详情；
 *   - 扫完（或用户停下）之后，这一行被替换成文件卡片，一次性铺开；
 *   - 用户中途停下时，已经扫到的那部分照样铺出来，目录行退场。
 *
 * 为什么不边扫边铺：一次拖进几万份文档时，卡片会像下雨一样往下掉，位置一直在变，
 * 用户既看不清进度也点不准；攒到收尾一次铺开，顺序稳定、总数一眼可见。
 *
 * 超时只按「多久没有任何动静」算（stall），不按「整批最多多久」：拖一整块盘本来
 * 就要走很久，那不是超时。
 */
import { markRaw } from 'vue'
import { cancelScan as requestCancelScan, describeError, scanStart as requestScanStart } from '../../api'
import type { FileResult, RemoteMode, ScanEvent } from '../../types'
import { t } from '../../i18n'
import { activePage, pageLocalId, pages, pageRows, rows } from './page'
import { pathKey, splitPath } from './paths'
import { reindexPage, seedImagesFromRows } from './rows'
import type { RunRow, ScanState } from './types'

/* ---------------- 扫描（每张卡片自己扫） ---------------- */

export interface ScanBatch {
  id: string
  mode: RemoteMode
  paths: string[]
  /** 兜底计时器：多久没有任何事件就认为这一批已经死了。 */
  timer: number | null
  /** 请求路径 → 扫到的文档。收尾时一次性铺成卡片。 */
  found: Map<string, FileResult[]>
  /** 已经收下的文件（按归一化路径），同一份不会进两次。 */
  seen: Set<string>
}

export const scanBatches = new Map<string, ScanBatch>()
export let scanSeq = 0

/**
 * 多久没有任何扫描事件就当这一批已经没了。
 *
 * 以前是「整批 26 秒上限」：拖一整块盘必然超时，界面上明明还在正常扫描，却被判成
 * 超时。现在改成按「静默多久」算 —— 只要有进度就一直等下去，真正的死连接三十秒
 * 就能发现。
 */
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

/** 这一页还有几行在扫。 */
export function pageScanningCount(mode: RemoteMode): number {
  return pageRows(mode).filter((row) => (row.scan ?? 'idle') === 'scanning').length
}

/** 把一份路径的扫描结果写进它自己那一行（只用于「拖进来的就是这一份文件」）。 */
function applyFileToRow(row: RunRow, file: FileResult): void {
  row.result = markRaw(file)
  row.images = file.images
  row.total = file.images
  row.missing = false
  row.scan = file.status === 'failed' ? 'failed' : 'ok'
  row.scanError = file.status === 'failed' ? file.error : undefined
  row.scanBatch = undefined
}

/**
 * 把一批扫描结果变成队列行。
 *
 * 已经在队列里的文件跳过：重复拖同一个目录、或者先拖文件再拖它所在的目录，
 * 都不该出现两张同名卡片。
 */
function buildRows(mode: RemoteMode, files: readonly FileResult[]): RunRow[] {
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
      images: file.images,
      phase: 'queued',
      uploaded: 0,
      total: file.images,
      // 扫描结果就是这一份的明细：展开行时逐图信息直接有出处。
      result: markRaw(file),
      scan: file.status === 'failed' ? 'failed' : 'ok',
      scanError: file.status === 'failed' ? file.error : undefined,
    })
  }
  return built
}

/**
 * 收掉一批：清计时器、把扫到的文档一次铺开、把没等到结果的行结掉。
 *
 * 「宿主行」指用户拖进来的那个目录：它自己不是文档，铺开之后就退场 ——
 * 文件已经各自成行。一份都没找到时留着它，好让用户知道这个目录里没有可处理的文档。
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

    // 这一份是文件本身，结果早在扫描时就落到行上了。
    if (row.result) {
      row.scan = 'ok'
      row.scanBatch = undefined
      continue
    }

    const found = batch.found.get(pathKey(path)) ?? []

    // 一份都没有：留着这张卡片说明情况（目录里没有可处理的文档，或者刚开扫就停了）。
    if (found.length === 0) {
      row.scan = state === 'cancelled' ? 'cancelled' : 'failed'
      row.scanError = state === 'cancelled' ? undefined : (message ?? t('scan.notFound'))
      row.scanBatch = undefined
      row.missing = state !== 'cancelled'
      continue
    }

    // 目录：一次性换成一叠文件卡片，位置就在它原来待的地方。
    const index = page.rows.indexOf(row)
    if (index < 0) continue
    page.rows.splice(index, 1, ...buildRows(batch.mode, found))
    changed = true
  }
  if (changed) reindexPage(page)
  if (batch.mode === activePage) seedImagesFromRows()
}

/**
 * 开扫。
 *
 * 立刻返回（不等结果）：Go 侧边遍历边推事件，界面把它们攒着，收尾时一次铺开。
 */
export async function scanPaths(mode: RemoteMode, paths: readonly string[]): Promise<void> {
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
  const batch: ScanBatch = { id, mode, paths: list, timer: null, found: new Map(), seen: new Set() }
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
    await requestScanStart({ scanId: id, paths: list })
  } catch (err) {
    finishBatch(batch, 'failed', describeError(err, t('scan.failed')))
  }
}

/** scan.progress / scan.file / scan.done 落到对应的卡片上。 */
export function onScanEvent(event: ScanEvent): void {
  const batch = scanBatches.get(event.runId)
  if (!batch) return // 这一批已经被取消/换掉了，晚到的事件直接丢

  if (event.type === 'scan.done') {
    finishBatch(batch, event.cancelled ? 'cancelled' : 'ok')
    return
  }

  // 有事件就说明还活着：把静默计时器往后推。
  touchBatch(batch, 'failed', t('scan.timeout'))

  const host = rowOfPage(batch.mode, event.path ?? '')
  if (!host) return
  if (host.scanBatch !== undefined && host.scanBatch !== batch.id) return // 上一批晚到的结果

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

  // 拖进来的就是这个文件：结果落在它自己那一行。
  if (files.length === 1 && pathKey(files[0].path) === pathKey(host.path)) {
    applyFileToRow(host, files[0])
    if (batch.mode === activePage) seedImagesFromRows()
    return
  }

  // 拖进来的是目录：先攒着，收尾时一次性铺开（见 finishBatch）。
  const key = pathKey(host.path)
  const bucket = batch.found.get(key) ?? []
  for (const file of files) {
    const fileKey = pathKey(file.path)
    if (batch.seen.has(fileKey)) continue
    batch.seen.add(fileKey)
    bucket.push(file)
  }
  batch.found.set(key, bucket)
}

/** 掐掉一批（整批或单个文件）。 */
export function requestBatchCancel(batch: ScanBatch, path?: string): void {
  void requestCancelScan(path === undefined ? { scanId: batch.id } : { scanId: batch.id, path }).catch(
    () => {
      // 取消是「尽力而为」：后端已经不认这一批了也算成功，界面上的状态早就改好了。
    },
  )
}

/** 取消某一张卡片的扫描（卡片上的「取消扫描」，确认框之后调用）。 */
export function cancelRowScan(id: string): void {
  const row = rows.value.find((item) => item.id === id)
  if (!row) return
  if ((row.scan ?? 'idle') !== 'scanning') return

  const key = pathKey(row.path)
  for (const batch of scanBatches.values()) {
    if (batch.mode !== activePage) continue
    if (!batch.paths.some((path) => pathKey(path) === key)) continue
    // 宿主行的收尾交给后端那条 scan.done：只有它知道这一批到这里为止发现了多少。
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

/** 重新扫一张卡片（取消/失败/超时之后）。 */
export function retryRowScan(id: string): void {
  const row = rows.value.find((item) => item.id === id)
  if (!row) return
  if ((row.scan ?? 'idle') === 'scanning') return
  void scanPaths(activePage, [row.path])
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
