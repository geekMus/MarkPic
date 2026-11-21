/**
 * 用户按下来的那几个按钮：开始、取消、清空、移除、加路径。
 *
 * 每一个都只管「这一页」（activePage）—— 起跑记下 owner，之后切页也不影响进度往哪写。
 * 请求都是异步的：飞在路上时用户可能已经切页，所以返回值一律用 withPage(owner, …) 写回。
 */
import { cancelRun as requestCancelRun, describeError, startRun as requestStartRun } from '../../api'
import type { RemoteMode, RunOp } from '../../types'
import { t } from '../../i18n'
import { useHealth } from '../useHealth'
import { useToasts } from '../useToasts'
import { connected } from './events'
import { activePage, nextLocalId, phase, rows, runId, summary, withPage } from './page'
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
    // 文件夹 / 盘扫描卡片不是这一轮的任务：它还没确认导入，不该被画成「排队中」。
    if (row.scanCard === true) continue
    row.phase = 'queued'
    row.uploaded = 0
    row.liveItems = undefined
    row.total = row.images ?? 0
    row.workTotal = workUnitsIn(row.result, activePage)
    row.workDone = 0
    // 上一轮的结果先留着：检测页/下载页的正文是「地址表」「远程→本地」，
    // 一开跑就清空会让正片在跑的时候变成空白页，看着像崩了。
    // 这一轮每跑完一个文件，事件会把这个文件的结果换掉。
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
  const added: string[] = []

  for (const raw of input) {
    const path = raw.trim().replace(/^"(.*)"$/, '$1').trim()
    if (path === '') continue
    const key = pathKey(path)
    if (key === '' || known.has(key)) continue
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

/** 页面 → 操作。三页各自对应的就是这三个动作，和设置里的 remote 无关。 */
const OP_OF_PAGE: Record<RemoteMode, RunOp> = {
  ignore: 'upload',
  check: 'check',
  download: 'download',
}

export async function start(paths: string[]): Promise<boolean> {
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
    const started = await requestStartRun(OP_OF_PAGE[owner], paths)
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