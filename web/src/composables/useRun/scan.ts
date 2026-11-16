/**
 * 每张卡片自己的扫描：发请求、按 scanId 收事件、结批。
 *
 * 扫描不再是一次 await 十个文件串着走 —— Go 侧扫完一个推一条事件，这里一批一个 scanId，
 * 事件带着 scanId 和路径回来，所以「哪个结果贴回哪张卡片」不用猜。批可以同时有好几批：
 * 掐掉一份再重扫就又开一批，互不干扰，晚到的事件靠 scanBatch 认领过滤。
 */
import { markRaw } from 'vue'
import { cancelScan as requestCancelScan, describeError, scanStart as requestScanStart } from '../../api'
import type { FileResult, RemoteMode, ScanEvent, Settings } from '../../types'
import { t } from '../../i18n'
import { activePage, pageRows, rows } from './page'
import { pathKey } from './paths'
import { seedImagesFromRows } from './rows'
import type { RunRow, ScanState } from './types'

/* ---------------- 扫描（每张卡片自己扫） ---------------- */

export interface ScanBatch {
  id: string
  mode: RemoteMode
  paths: string[]
  /** 兜底计时器：整批迟迟没有收尾事件时把它按超时结掉。 */
  timer: number | null
}

/**
 * 正在跑的扫描批次。
 *
 * 一批一个 scanId，Go 侧每扫完一个文件推一条 scan.file，事件里带着这个 scanId
 * 和文件路径 —— 所以「哪个结果贴回哪张卡片」不用猜。表里可以同时有多批：
 * 用户掐掉一份再点重新扫描，就又是一批，两批互不干扰。
 */
export const scanBatches = new Map<string, ScanBatch>()
export let scanSeq = 0

/**
 * 整批扫描的兜底时限。
 *
 * Go 侧每个文件 20 秒封顶，这里再宽一点。之所以还要一层：后台进程可能压根没接上
 * 窗口（刚启动、WebView 还在加载），事件一条都送不到，界面不能因此永远点不了「开始」。
 */
export const SCAN_BATCH_TIMEOUT_MS = 26_000

export function rowOfPage(mode: RemoteMode, path: string): RunRow | undefined {
  const key = pathKey(path)
  return pageRows(mode).find((row) => pathKey(row.path) === key)
}

/**
 * 把一份文件（或一个目录下的若干文件）的扫描结果写到行上。
 *
 * 目录行没有「自己」的结果 —— 前缀命中的那些加起来就是它的张数，和以前
 * preview 的做法一致；一份都没命中说明路径不存在或目录里没有 Markdown。
 */
export function writeScanResult(row: RunRow, files: readonly FileResult[]): void {
  const key = pathKey(row.path)
  const exact = files.find((file) => pathKey(file.path) === key)
  if (exact) {
    if (!row.result) row.result = markRaw(exact)
    row.images = exact.images
    row.total = exact.images
    row.missing = false
    row.isDir = false
    return
  }

  const prefix = `${key}\\`
  let sum = 0
  let matched = false
  for (const file of files) {
    if (pathKey(file.path).startsWith(prefix)) {
      sum += file.images
      matched = true
    }
  }
  if (matched) {
    row.images = sum
    row.total = sum
    row.missing = false
    row.isDir = true
  } else {
    row.missing = true
  }
}

/** 收掉一批：清计时器、从表里摘掉，把没等到结果的行按 state 结掉。 */
export function finishBatch(batch: ScanBatch, state: ScanState, message?: string): void {
  if (batch.timer !== null) {
    window.clearTimeout(batch.timer)
    batch.timer = null
  }
  scanBatches.delete(batch.id)
  for (const path of batch.paths) {
    const row = rowOfPage(batch.mode, path)
    if (!row) continue
    if ((row.scan ?? 'idle') !== 'scanning') continue
    if (row.scanBatch !== batch.id) continue // 已经重扫过：这一批管不着它了
    row.scan = state
    row.scanBatch = undefined
    if (message) row.scanError = message
  }
  if (batch.mode === activePage) seedImagesFromRows()
}

/**
 * 开扫。
 *
 * 立刻返回（不等结果）：Go 侧现在是半个文件一个协程、按核数限流，扫完一个推一条
 * 事件回来。以前这里是 await 一次 preview，十个文件串着扫，界面整段冻住，
 * 卡片也只能等全部扫完才出现。
 */
export async function scanPaths(
  mode: RemoteMode,
  paths: readonly string[],
  settings: Settings,
): Promise<void> {
  const list: string[] = []
  const seen = new Set<string>()
  for (const raw of paths) {
    const path = raw.trim()
    if (path === '' || seen.has(pathKey(path))) continue
    seen.add(pathKey(path))
    list.push(path)
  }
  if (list.length === 0) return

  const id = `scan-${Date.now().toString(36)}-${++scanSeq}`
  const batch: ScanBatch = { id, mode, paths: list, timer: null }
  scanBatches.set(id, batch)

  for (const path of list) {
    const row = rowOfPage(mode, path)
    if (!row) continue
    row.scan = 'scanning'
    row.scanError = undefined
    row.scanBatch = batch.id
  }

  batch.timer = window.setTimeout(() => {
    batch.timer = null
    finishBatch(batch, 'timeout', t('scan.timeout'))
  }, SCAN_BATCH_TIMEOUT_MS)

  try {
    await requestScanStart({ scanId: id, paths: list, settings })
  } catch (err) {
    finishBatch(batch, 'failed', describeError(err, t('scan.failed')))
  }
}

/** scan.file / scan.done 落到对应的卡片上。 */
export function onScanEvent(event: ScanEvent): void {
  const batch = scanBatches.get(event.runId)
  if (!batch) return // 这一批已经被取消/换掉了，晚到的事件直接丢

  if (event.type === 'scan.done') {
    // 正常情况下这时每行都已经有自己的结果；还停在「扫描中」的行说明事件丢了，
    // 按超时结掉，别让「开始」永远灰着。
    finishBatch(batch, event.cancelled ? 'cancelled' : 'timeout', t('scan.timeout'))
    return
  }

  const row = rowOfPage(batch.mode, event.path ?? '')
  if (!row) return
  if (row.scanBatch !== undefined && row.scanBatch !== batch.id) return // 上一批晚到的结果

  if (event.cancelled) {
    row.scan = 'cancelled'
    row.scanError = undefined
    row.scanBatch = undefined
    row.images = null
    return
  }
  if (event.timedOut) {
    row.scan = 'timeout'
    row.scanError = t('scan.timeout')
    row.scanBatch = undefined
    return
  }

  const files = event.scanned ?? []
  if (files.length === 0) {
    row.scan = 'failed'
    row.scanError = t('scan.notFound')
    row.scanBatch = undefined
    row.missing = true
    return
  }

  writeScanResult(row, files)
  row.scan = 'ok'
  row.scanError = undefined
  row.scanBatch = undefined
  if (batch.mode === activePage) seedImagesFromRows()
}

/** 掐掉一批（整批或单个文件）。 */
export function requestBatchCancel(batch: ScanBatch, path?: string): void {
  void requestCancelScan(path === undefined ? { scanId: batch.id } : { scanId: batch.id, path }).catch(
    () => {
      // 取消是「尽力而为」：后端已经不认这一批了也算成功，界面上的状态早就改好了。
    },
  )
}

/** 取消某一张卡片的扫描（卡片上的「取消扫描」）。 */
export function cancelRowScan(id: string): void {
  const row = rows.value.find((item) => item.id === id)
  if (!row) return
  if ((row.scan ?? 'idle') !== 'scanning') return

  row.scan = 'cancelled'
  row.scanError = undefined
  row.scanBatch = undefined
  row.images = null
  const key = pathKey(row.path)
  for (const batch of scanBatches.values()) {
    if (batch.mode !== activePage) continue
    if (!batch.paths.some((path) => pathKey(path) === key)) continue
    requestBatchCancel(batch, row.path)
    return
  }
}

/** 重新扫一张卡片（取消/失败/超时之后）。 */
export function retryRowScan(id: string, settings: Settings): void {
  const row = rows.value.find((item) => item.id === id)
  if (!row) return
  if ((row.scan ?? 'idle') === 'scanning') return
  void scanPaths(activePage, [row.path], settings)
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

/** 这一页还有几行在扫（页头写「正在扫描 3/10」用）。 */
export function pageScanningCount(mode: RemoteMode): number {
  return pageRows(mode).filter((row) => (row.scan ?? 'idle') === 'scanning').length
}
