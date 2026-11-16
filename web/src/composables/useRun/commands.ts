/**
 * 用户按下来的那几个按钮：开始、取消、清空、移除、加路径。
 *
 * 每一个都只管「这一页」（activePage）—— 起跑记下 owner，之后切页也不影响进度往哪写。
 * 请求都是异步的：飞在路上时用户可能已经切页，所以返回值一律用 withPage(owner, …) 写回。
 */
import { markRaw } from 'vue'
import { cancelRun as requestCancelRun, describeError, startRun as requestStartRun } from '../../api'
import type { PreviewResult, RemoteMode, Settings } from '../../types'
import { t } from '../../i18n'
import { useHealth } from '../useHealth'
import { useToasts } from '../useToasts'
import { connected } from './events'
import { activePage, nextLocalId, pages, phase, rows, runId, savePage, summary, withPage } from './page'
import { pathKey, splitPath } from './paths'
import { cancelTimers, clearCancelTimer, clearRunning, markRunning, runningDebug, runningModes } from './running'
import {
  liveReset,
  markUnsettledCancelled,
  pushRow,
  reindexRows,
  seedImagesFromRows,
  workUnitsIn,
} from './rows'
import { cancelPageScans, cancelRowScan } from './scan'

const { danger, warning } = useToasts()

/** 把每一行恢复到「还没跑」的样子（行对象原地改，不重建数组）。 */
export function resetRowsForRun(): void {
  for (const row of rows.value) {
    row.phase = 'queued'
    row.uploaded = 0
    row.liveItems = undefined
    row.counted = undefined
    row.total = row.images ?? 0
    row.workTotal = workUnitsIn(row.result, activePage)
    row.workDone = 0
    // 上一轮的结果先留着：检测页/下载页的正文是「地址表」「远程→本地」，
    // 一开跑就清空会让正片在跑的时候变成空白页，看着像崩了。
    // 这一轮每跑完一个文件，事件会把这个文件的结果换掉。
    row.scored = false
  }
  liveReset()
  seedImagesFromRows()
}

export function resetOutcome(): void {
  phase.value = 'idle'
  summary.value = null
  runId.value = null
  resetRowsForRun()
}

/** 加入队列；返回真正新增的路径，供调用方做预览。 */
export function addPaths(input: string[]): string[] {
  if (runningModes.has(activePage)) {
    warning(t('toast.queueLocked'))
    return []
  }

  if (phase.value === 'done' || phase.value === 'cancelled') resetOutcome()

  const known = new Set(rows.value.map(row => pathKey(row.path)))
  // 已经在队列里的目录，它下面的文件开跑时会被那个目录一起扫出来。
  // 这时候再单独加一份文件，等于同一篇文档进队列两次：页脚会把图片数算两遍，
  // 跑完还会看到两行同名结果。所以目录里的路径直接并进那行目录，不再新开一行。
  const dirPrefixes = rows.value.filter(row => row.isDir).map(row => pathKey(row.path) + '\\')
  const added: string[] = []

  for (const raw of input) {
    const path = raw.trim().replace(/^"(.*)"$/, '$1').trim()
    if (path === '') continue
    const key = pathKey(path)
    if (key === '' || known.has(key)) continue
    if (dirPrefixes.some(prefix => key.startsWith(prefix))) continue
    known.add(key)
    added.push(path)
    const { name, dir } = splitPath(path)
    pushRow({
      id: nextLocalId(),
      path,
      name,
      dir,
      images: null,
      phase: 'queued',
      uploaded: 0,
      total: 0,
      result: null,
    })
  }

  return added
}

/**
 * 用 /api/preview 的结果补齐队列行的图片数量（目录行按前缀汇总）。
 *
 * 只更新这一次真的预检过的行：预览请求带的是刚加进来的那几个路径，返回的也只有它们
 * （以及目录里的文件）。早先对每一行都套这个结果，于是「先加一个目录、再加一个别的文件」
 * 会把那个目录的图片数从 4 改成 1，甚至把它标成「找不到」—— 新的预览结果里本来就没有它。
 */
/**
 * 把预检结果写进某一页。
 *
 * 启动恢复时三页是连着扫的，扫到第三页时画面可能还在第一页 —— 所以结果要按「哪一页」
 * 落，不能按「当前 rows」落，否则预检白扫（行上的张数永远停在「待统计」）。
 */
export function applyPreviewOn(mode: RemoteMode, result: PreviewResult, touched: readonly string[]): void {
  if (mode === activePage) savePage()
  const byPath = new Map(result.files.map(file => [pathKey(file.path), file]))
  const touchedKeys = new Set(touched.map(pathKey))

  for (const row of pages[mode].rows) {
    const rowKey = pathKey(row.path)
    if (!touchedKeys.has(rowKey) && !byPath.has(rowKey)) continue
    const exact = byPath.get(pathKey(row.path))
    if (exact) {
      // 预检结果也留在行上：行的 images 有了，逐图明细（L 行）才有出处，
      // 否则展开一行 8 张图的文件会得到「没有可处理的本地图片」这种自相矛盾的话。
      if (!row.result) row.result = markRaw(exact)
      row.images = exact.images
      row.total = exact.images
      row.missing = false
      row.isDir = false
      continue
    }

    const prefix = `${pathKey(row.path)}\\`
    let sum = 0
    let matched = false
    for (const file of result.files) {
      if (pathKey(file.path).startsWith(prefix)) {
        sum += file.images
        matched = true
      }
    }
    // 预览返回的是「真正找到的文件」；什么都没匹配上说明这个路径不存在，
    // 或者目录里没有 Markdown —— 明确说出来，别让行停在「待统计」。
    if (matched) {
      row.images = sum
      row.total = sum
      row.missing = false
      // 按前缀匹配上的，说明这一行是目录而不是某个具体文档。
      row.isDir = true
    } else {
      row.missing = true
    }
  }

  if (mode === activePage) seedImagesFromRows()
}

export async function start(paths: string[], settings: Settings): Promise<boolean> {
  const owner = activePage
  if (runningModes.has(owner)) return false
  if (paths.length === 0) {
    warning(t('toast.queueEmpty'))
    return false
  }

  clearCancelTimer(owner)
  phase.value = 'preparing'
  runId.value = null
  summary.value = null
  resetRowsForRun()
  // 这一轮属于「开始它的那一页」：之后怎么切页都不影响它的进度往哪儿写。
  markRunning(owner, null)
  // 调试模式来自构建标记（health.debug），不是设置项。
  runningDebug.value = useHealth().health.value?.debug === true

  if (!connected.value) {
    warning(t('toast.eventsNotReady'))
  }

  try {
    const started = await requestStartRun(paths, settings)
    if (typeof started.runId !== 'string' || started.runId === '') {
      throw new Error(t('error.noRunId'))
    }
    // 请求飞在路上时用户可能已经切页：返回值要写回开始它的那一页。
    withPage(owner, () => {
      // 这一轮可能已经跑完（事件比返回值先到）：收工了就别再把它标成在跑。
      if (phase.value !== 'preparing' && phase.value !== 'running') return
      markRunning(owner, started.runId)
      if (runId.value === null) runId.value = started.runId
      if (phase.value === 'preparing') phase.value = 'running'
    })
    return true
  } catch (err) {
    let startedAnyway = false
    let failedHere = false
    withPage(owner, () => {
      if (runId.value !== null) {
        startedAnyway = true
        return
      }
      phase.value = 'idle'
      clearRunning(owner)
      failedHere = true
    })
    if (!startedAnyway && failedHere) danger(describeError(err, t('error.startRun')))
    return startedAnyway
  }
}

export async function cancel(): Promise<void> {
  const owner = activePage
  if (phase.value !== 'running') return
  if (runId.value === null) {
    warning(t('toast.noRunId'))
    return
  }

  phase.value = 'cancelling'
  const cancellingRunId = runId.value
  clearCancelTimer(owner)
  cancelTimers.set(
    owner,
    window.setTimeout(() => {
      cancelTimers.delete(owner)
      withPage(owner, () => {
        if (phase.value !== 'cancelling' || runId.value !== cancellingRunId) return
        phase.value = 'cancelled'
        clearRunning(owner, cancellingRunId)
        markUnsettledCancelled()
        runId.value = null
        warning(t('toast.cancelUnconfirmed'))
      })
    }, 5000),
  )

  try {
    await requestCancelRun(cancellingRunId)
  } catch (err) {
    withPage(owner, () => {
      if (phase.value !== 'cancelling' || runId.value !== cancellingRunId) return
      clearCancelTimer(owner)
      phase.value = 'running'
      danger(describeError(err, t('error.cancelRun')))
    })
  }
}

export function removeRow(id: string): void {
  if (runningModes.has(activePage)) return
  // 正在扫的那一份先掐掉：行都没了，后台还在读盘就是白干。
  cancelRowScan(id)
  rows.value = rows.value.filter(row => row.id !== id)
  reindexRows()
  if (rows.value.length === 0) {
    phase.value = 'idle'
    summary.value = null
    runId.value = null
    liveReset()
  }
}

export function clearQueue(): void {
  if (runningModes.has(activePage)) {
    warning(t('toast.clearLocked'))
    return
  }
  cancelPageScans()
  clearCancelTimer()
  rows.value = []
  reindexRows()
  liveReset()
  summary.value = null
  runId.value = null
  phase.value = 'idle'
}
