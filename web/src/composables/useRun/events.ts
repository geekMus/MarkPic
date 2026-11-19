/**
 * 事件流：从 Go 侧接住事件、攒帧、分发到它自己那一页。
 *
 * 三页可以同时在跑，事件只带 runId：run.started 带 mode，先把 runId 记到那一页，之后这条
 * runId 的所有事件都认领回同一页 —— 哪怕用户正看着别的页。事件先攒后放（每 16ms 一批、
 * 一批最多 200 条），否则一千条事件就是一千次 Vue 更新加一千次重排。
 *
 * 统计数字不在这里算：后端每个事件都带着「这一轮到目前为止」的累计汇总（event.summary），
 * 这里只负责把它放到当前页上。以前前端按二十几个字段各加减一遍，跑完再被后端那份汇总
 * 整个盖掉 —— 同一份数字两个算法，口径稍有出入就当场对不上。
 */
import { ref } from 'vue'
import type { RemoteMode, RunEvent, RunOp, RunSummary } from '../../types'
import { t } from '../../i18n'
import { useToasts } from '../useToasts'
import { activePage, live, phase, runId, summary, withPage } from './page'
import { busy, clearCancelTimer, clearRunning, markRunning, modeOfRun, runningDebug } from './running'
import {
  absorbResult,
  applyStarted,
  cancelText,
  markUnsettledCancelled,
  pushRow,
  rowAt,
  rowForResult,
  rowFromResult,
  seedLiveItems,
  updateRow,
} from './rows'
import { onScanEvent } from './scan'
import { KNOWN_EVENT_TYPES } from './types'

const { danger, success, warning } = useToasts()

export const connected = ref(false)

export let sinkActive = false
// 一轮只播报一次结局：服务端在「自己掐掉」时会先发 run.cancelled 再发 run.finished，
// 两条都带 summary.abortReason，不拦一下就会弹出两条一模一样的气泡。
export const announcedRuns = new Set<string>()

/** 一轮在后台跑完时让任务栏闪一下：人切去干别的了，总得有个机会注意到。 */
export function flashIfAway(): void {
  if (document.hasFocus()) return
  const bridge = window as unknown as { cmpWinFlash?: () => Promise<unknown> }
  void bridge.cmpWinFlash?.()
}

export function isRunEvent(value: unknown): value is RunEvent {
  if (typeof value !== 'object' || value === null) return false
  const record = value as Record<string, unknown>
  const type = record.type
  if (typeof type !== 'string' || !KNOWN_EVENT_TYPES.has(type)) return false
  if (type === 'log') {
    return typeof record.level === 'string' && typeof record.message === 'string'
  }
  return typeof record.runId === 'string'
}

/**
 * run.started 带的是操作名（upload / check / download），页面叫 ignore / check / download。
 * 两者不是同一套取值：一个说的是「用户点了什么」，一个说的是「文档里的网络图片怎么办」。
 */
const PAGE_OF_OP: Record<RunOp, RemoteMode> = {
  upload: 'ignore',
  check: 'check',
  download: 'download',
}

export function matches(eventRunId: string): boolean {
  return runId.value !== null && runId.value === eventRunId
}

/**
 * 把后端带回来的累计汇总落到这一页上。
 *
 * 只做一件事：覆盖。数字是后端算的，界面不再自己加减 —— 这也是「跑动中的数和跑完的数
 * 永远一致」的唯一保证。
 */
function applySummary(next: RunSummary | undefined): void {
  if (!next) return
  summary.value = next
  live.images = next.images
  live.uploaded = next.uploaded
  live.reused = next.reused
  live.uploadRequests = next.uploadRequests ?? 0
  live.failed = next.failedImages
  live.ok = next.ok
  live.partial = next.partial
  live.failedFiles = next.failed
  live.noImages = next.noImages
  live.localized = next.localized ?? 0
  live.localizedUnique = next.localizedUnique ?? 0
  live.remoteChecked = next.remoteChecked ?? 0
  live.remoteCheckedUnique = next.remoteCheckedUnique ?? 0
  live.remoteDead = next.remoteDead ?? 0
  live.remoteDeadUnique = next.remoteDeadUnique ?? 0
  live.remoteFailed = next.remoteFailed ?? 0
  live.remoteFailedUnique = next.remoteFailedUnique ?? 0
  live.remoteUnsupported = next.remoteUnsupported ?? 0
  live.localMissing = next.localMissing ?? 0
  live.remoteKept = next.remoteKept ?? 0
  live.filesChecked = next.filesChecked ?? 0
  live.remoteReused = next.remoteReused ?? 0
  live.abortReason = next.abortReason ?? ''
}

/**
 * 把一条事件送回它自己那一页。
 *
 * 三页可以同时在跑，事件只带 runId；run.started 带 mode，先把 runId 记到那一页，
 * 之后这条 runId 的所有事件都认领回同一页 —— 哪怕用户正看着别的页。
 */
export function onSignal(event: RunEvent): void {
  // 扫描事件不参和「一轮转换」：它有自己的 scanId、自己的卡片状态。
  if (event.type === 'scan.file' || event.type === 'scan.done') {
    onScanEvent(event)
    return
  }
  const startedOp = event.type === 'run.started' ? event.mode : undefined
  const owner =
    startedOp !== undefined && startedOp in PAGE_OF_OP
      ? PAGE_OF_OP[startedOp as RunOp]
      : 'runId' in event
        ? modeOfRun(event.runId)
        : null
  if (owner === null) {
    // 日志没有页归属：还有活干就按当前页提示一声，别把后台的报错吞掉。
    if (event.type === 'log' && busy.value) applyRunEvent(event, activePage)
    return
  }
  if (owner === activePage) applyRunEvent(event, owner)
  else withPage(owner, () => applyRunEvent(event, owner))
}

export function applyRunEvent(event: RunEvent, owner: RemoteMode): void {
  switch (event.type) {
    case 'run.started': {
      if (runId.value !== null && runId.value !== event.runId) return
      if (runId.value === null && phase.value !== 'preparing') return
      runId.value = event.runId
      markRunning(owner, event.runId)
      applyStarted(event.files)
      summary.value = null
      phase.value = 'running'
      return
    }
    case 'file.started': {
      if (!matches(event.runId)) return
      updateRow(event.id, { phase: 'running' })
      const row = rowAt(event.id)
      // 先把扫描到的明细铺开：第一张图处理完之前，卡片上也该看得到「待取回 7」。
      if (row) seedLiveItems(row)
      return
    }
    case 'file.progress': {
      if (!matches(event.runId)) return
      const row = rowAt(event.id)
      if (!row) return
      // 进度是「这一篇处理了几张图」，不是「替换了几处」：只检测/只下载时上传数是 0，
      // 以前把进度累加进 uploaded，页脚会闪出「替换 13」这种没发生过的数字。
      row.phase = 'running'
      row.done = event.done ?? 0
      row.total = event.total
      row.images = event.total
      row.workTotal = event.total
      row.workDone = Math.min(event.done ?? 0, event.total)

      // 增量只带「刚处理完的是哪几处」：按位置覆盖，卡片上那一行就从「待取回」变成
      // 「已取回」/「失败」。整份重发的话，一篇上万条明细会把事件通道撑爆。
      seedLiveItems(row)
      if (row.liveItems && event.items?.length) {
        for (const patch of event.items) {
          if (patch.index < 0 || patch.index >= row.liveItems.length) continue
          row.liveItems[patch.index] = patch.item
        }
      }
      // 页脚那几个数由后端算好了随事件带回来，这里只覆盖。
      applySummary(event.summary)
      return
    }
    case 'file.finished': {
      if (!matches(event.runId)) return
      let row = rowForResult(event.id, event.result.path)
      if (!row) {
        row = rowFromResult(event.result)
        row.serverId = event.id
        pushRow(row)
      }
      absorbResult(row, event.result)
      applySummary(event.summary)
      return
    }
    case 'run.finished': {
      if (!matches(event.runId)) {
        // 开始请求还没返回、run.started 先到：这一页还停在「准备中」，认下这条收尾。
        if (runId.value !== null || phase.value !== 'preparing') return
        runId.value = event.runId
      }
      clearRunning(owner, event.runId)
      clearCancelTimer(owner)
      if (announcedRuns.has(event.runId)) {
        runId.value = null
        phase.value = 'cancelled'
        return
      }
      flashIfAway()
      summary.value = event.summary
      applySummary(event.summary)
      phase.value = event.summary.cancelled ? 'cancelled' : 'done'
      if (event.summary.cancelled) markUnsettledCancelled()
      runId.value = null
      announceOutcome(owner, event.summary, event.runId)
      return
    }
    case 'run.cancelled': {
      if (!matches(event.runId)) return
      clearRunning(owner, event.runId)
      clearCancelTimer(owner)
      if (event.summary) applySummary(event.summary)
      phase.value = 'cancelled'
      markUnsettledCancelled()
      runId.value = null
      if (announcedRuns.has(event.runId)) return
      announcedRuns.add(event.runId)
      flashIfAway()
      if (event.summary?.abortReason) danger(event.summary.abortReason)
      else warning(cancelText(owner))
      return
    }
    case 'log': {
      if (!busy.value) return
      if (event.level === 'error') danger(event.message)
      else if (event.level === 'warn') warning(event.message)
      else console.info('[server]', event.message)
      return
    }
  }
}

/** 一轮跑完之后的那句提示：按这一轮到底做了什么说，别让下载页说「上传完成」。 */
function announceOutcome(owner: RemoteMode, sum: RunSummary, eventRunId: string): void {
  if (sum.cancelled) {
    // 图床成片失败时这一轮是我们自己掐掉的，得把原因摆出来，
    // 否则用户只看到「已取消」，以为是自己点错了。
    announcedRuns.add(eventRunId)
    if (sum.abortReason) danger(sum.abortReason)
    else warning(cancelText(owner))
    return
  }
  if (owner === 'download') {
    const got = sum.localized ?? 0
    const failed = (sum.remoteFailed ?? 0) + (sum.remoteDead ?? 0)
    if (got > 0 && failed > 0) warning(t('toast.runDownloadedPartial', { count: got, failed }))
    else if (got > 0) success(t('toast.runDownloaded', { count: got }))
    else if (failed > 0) warning(t('toast.runDownloadedFailed', { failed }))
    else if (runningDebug.value && (sum.remoteKept ?? 0) > 0)
      // 调试构建确实一张都没下，但说「没有可下载的远程图片」会让人以为扫描没扫到东西。
      warning(t('toast.runDownloadedDebug', { count: sum.remoteKept ?? 0 }))
    else warning(t('toast.runDownloadedNone'))
    return
  }
  if (runningDebug.value) {
    // 调试构建整轮只扫描、不上传：这里落到「上传完成 · 0 张图片」像是什么都没找到。
    warning(t('toast.runFinishedDebug'))
    return
  }
  if (sum.failedImages > 0) {
    warning(t('toast.runFinishedPartial', { count: sum.failedImages }))
    return
  }
  const requests = sum.uploadRequests ?? 0
  if (requests > 0 && requests < sum.uploaded) {
    // 同一份内容被多个名字引用时只上行一次：「替换 3 处」不等于「发了 3 张」。
    success(t('toast.runFinishedDedup', { refs: sum.uploaded, requests }))
    return
  }
  if (sum.uploaded === 0 && (sum.localized ?? 0) > 0) {
    // 只下载的一轮：「下载完成」才说得清这一轮做了什么。
    success(t('toast.runDownloaded', { count: sum.localized ?? 0 }))
    return
  }
  if (sum.uploaded === 0 && (sum.remoteChecked ?? 0) > 0) {
    // 只检测的一轮：说清是几个地址、坏没坏，而不是「上传 0 张图片」。
    const dead = sum.remoteDead ?? 0
    if (dead > 0) warning(t('toast.runCheckedDead', { count: dead }))
    else success(t('toast.runCheckedClean', { count: sum.remoteChecked ?? 0 }))
    return
  }
  success(t('toast.runFinished', { count: sum.uploaded }))
}

/**
 * 事件先攒起来，每帧统一应用一次。
 *
 * 大语料一轮会推来上千条事件（每篇文件的 started/progress/finished），逐条应用
 * 就是逐条触发 Vue 更新与列表重渲染；攒成一帧一批之后，渲染次数降到每秒几十次。
 * 用定时器而不是 requestAnimationFrame：窗口最小化时 rAF 根本不触发，而定时器
 * 仍会跑（只是被节流），不会把事件一直闷在缓冲区里。
 */
export const EVENT_FLUSH_MS = 16

/**
 * 一次最多应用多少条事件。
 *
 * 服务端是按批下发的，一批可能有上千条。一次全应用会让 Vue 在同一个 tick 里
 * 更新上千个组件、浏览器紧接着同步重排整张表——5000 篇那次实测出现过 5.7 秒的
 * 单帧停顿。分片之后每帧的工作量有上限，剩下的下一帧接着做。
 */
export const EVENT_FLUSH_MAX = 200

export const pending: RunEvent[] = []
export let pendingTimer: number | null = null

export function flushPending(): void {
  pendingTimer = null
  if (pending.length === 0) return
  const batch = pending.splice(0, EVENT_FLUSH_MAX)
  for (const event of batch) onSignal(event)
  if (pending.length > 0) {
    pendingTimer = window.setTimeout(flushPending, EVENT_FLUSH_MS)
  }
}

export function enqueue(event: RunEvent): void {
  pending.push(event)
  if (pendingTimer === null) {
    pendingTimer = window.setTimeout(flushPending, EVENT_FLUSH_MS)
  }
}

export function connect(): void {
  if (sinkActive) return
  sinkActive = true

  // 事件不再走 SSE：Go 侧把攒好的批次直接调到这里（见 cmd/markpic/window_windows.go
  // 的 bindBridge）。一批是一个数组，元素仍是原来那些事件对象。
  window.__markpicEvents = (payload: unknown) => {
    const list = Array.isArray(payload) ? payload : [payload]
    for (const item of list) {
      if (!isRunEvent(item)) {
        console.warn('[events] 忽略未知事件')
        continue
      }
      enqueue(item)
    }
  }

  connected.value = true
}

export function disconnect(): void {
  sinkActive = false
  window.__markpicEvents = undefined
  connected.value = false
}
