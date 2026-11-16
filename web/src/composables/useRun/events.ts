/**
 * 事件流：从 Go 侧接住事件、攒帧、分发到它自己那一页。
 *
 * 三页可以同时在跑，事件只带 runId：run.started 带 mode，先把 runId 记到那一页，之后这条
 * runId 的所有事件都认领回同一页 —— 哪怕用户正看着别的页。事件先攒后放（每 16ms 一批、
 * 一批最多 200 条），否则一千条事件就是一千次 Vue 更新加一千次重排。
 */
import { ref } from 'vue'
import { getCurrentRun } from '../../api'
import type { CurrentRun, RemoteMode, RunEvent } from '../../types'
import { t } from '../../i18n'
import { useToasts } from '../useToasts'
import { activePage, adopted, live, phase, rows, runId, rowIndex, summary, withPage } from './page'
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
import { KNOWN_EVENT_TYPES, PAGES } from './types'

const { danger, success, warning } = useToasts()

export const connected = ref(false)
export const reconnecting = ref(false)

export let sinkActive = false
export let reconnectTimer: number | null = null
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

export function matches(eventRunId: string): boolean {
  return runId.value !== null && runId.value === eventRunId
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
  const owner =
    event.type === 'run.started' && (event.mode === 'ignore' || event.mode === 'check' || event.mode === 'download')
      ? event.mode
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
      live.images += event.total - (row.images ?? row.total)
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
        const counted = row.counted ?? (row.counted = new Set<number>())
        for (const patch of event.items) {
          if (patch.index < 0 || patch.index >= row.liveItems.length) continue
          // 页脚的「替换」也按刚处理完的引用走：卡片显示「已替换 4」而页脚还是
          // 「替换 0」会像没动。每处只记一次，进度事件重放不会把数字算重。
          const status = patch.item.status
          if (
            !counted.has(patch.index) &&
            (status === 'uploaded' || status === 'reused' || status === 'cached')
          ) {
            counted.add(patch.index)
            row.uploaded += 1
            live.uploaded += 1
          }
          row.liveItems[patch.index] = patch.item
        }
      }
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
      if (announcedRuns.has(event.runId)) { runId.value = null; phase.value = 'cancelled'; return }
      flashIfAway()
      summary.value = event.summary
      phase.value = event.summary.cancelled ? 'cancelled' : 'done'
      if (event.summary.cancelled) markUnsettledCancelled()
      runId.value = null
      if (event.summary.cancelled) {
        // 图床成片失败时这一轮是我们自己掐掉的，得把原因摆出来，
        // 否则用户只看到「已取消」，以为是自己点错了。
        announcedRuns.add(event.runId)
        if (event.summary.abortReason) danger(event.summary.abortReason)
        else warning(cancelText(owner))
      } else if (owner === 'download') {
        // 下载页用自己的口径：取回几张、几个地址没下下来。
        // 以前这里会落到「上传完成」「检测完成」的文案上——下载页说「检测完成」不对，
        // 说「上传完成 · 0 张图片」更不对。
        const got = event.summary.localized ?? 0
        const failed = (event.summary.remoteFailed ?? 0) + (event.summary.remoteDead ?? 0)
        if (got > 0 && failed > 0) warning(t('toast.runDownloadedPartial', { count: got, failed }))
        else if (got > 0) success(t('toast.runDownloaded', { count: got }))
        else if (failed > 0) warning(t('toast.runDownloadedFailed', { failed }))
        else if (runningDebug.value && (event.summary.remoteKept ?? 0) > 0)
          // 调试构建确实一张都没下，但说「没有可下载的远程图片」会让人以为扫描没扫到东西。
          warning(t('toast.runDownloadedDebug', { count: event.summary.remoteKept ?? 0 }))
        else warning(t('toast.runDownloadedNone'))
      } else if (runningDebug.value) {
        // 调试构建整轮只扫描、不上传：这里落到「上传完成 · 0 张图片」像是什么都没找到。
        warning(t('toast.runFinishedDebug'))
      } else if (event.summary.failedImages > 0) {
        warning(t('toast.runFinishedPartial', { count: event.summary.failedImages }))
      } else if (
        (event.summary.uploadRequests ?? 0) > 0 &&
        (event.summary.uploadRequests ?? 0) < event.summary.uploaded
      ) {
        // 同一份内容被多个名字引用时只上行一次：「替换 3 处」不等于「发了 3 张」。
        success(
          t('toast.runFinishedDedup', {
            refs: event.summary.uploaded,
            requests: event.summary.uploadRequests ?? 0,
          }),
        )
      } else if (event.summary.uploaded === 0 && (event.summary.localized ?? 0) > 0) {
        // 只下载的一轮：「下载完成」才说得清这一轮做了什么。
        success(t('toast.runDownloaded', { count: event.summary.localized ?? 0 }))
      } else if (event.summary.uploaded === 0 && (event.summary.remoteChecked ?? 0) > 0) {
        // 只检测的一轮：说清是几个地址、坏没坏，而不是「上传 0 张图片」。
        const dead = event.summary.remoteDead ?? 0
        if (dead > 0) warning(t('toast.runCheckedDead', { count: dead }))
        else success(t('toast.runCheckedClean', { count: event.summary.remoteChecked ?? 0 }))
      } else {
        success(t('toast.runFinished', { count: event.summary.uploaded }))
      }
      return
    }
    case 'run.cancelled': {
      if (!matches(event.runId)) return
      clearRunning(owner, event.runId)
      clearCancelTimer(owner)
      if (event.summary) summary.value = event.summary
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

/**
 * 把 Go 侧记着的「这一轮」接管过来。
 *
 * 用在两种时刻：页面刚打开（刷新过，内存里的 runId 已经没了）、以及断线重连之后
 * （断的那段时间里 file.finished 全丢了）。文件清单来自快照，已完成的结果直接贴回
 * 对应的行，还在跑的标成「上传中」。
 *
 * 但「上一次会话已经跑完的那一轮」不接管：它属于历史，不是这一页的队列。刚打开应用
 * 时页面上还没有任何行，接管它会把别的页的文件搬过来、把本页队列整个盖掉。
 */
export function adoptSnapshot(mode: RemoteMode, snapshot: CurrentRun): void {
  if (!snapshot.runId) return
  // 这一轮的结果已经在界面上了（自己刚跑完），不要用快照再盖一遍。
  if (summary.value !== null && summary.value.runId === snapshot.runId) return
  // 已经结束、而且这个页面早就见过它（比如用户跑完又清空了队列）：
  // 重连时别把旧结果重新变出来。
  if (!snapshot.running && adopted.runId === snapshot.runId) return
  // 上一次会话已经收工的一轮不进队列：页面上还没有行时接管它，等于把那一轮的文件
  // 当成这一页的队列（还会把 localStorage 里别的页的路径覆盖掉）。
  if (!snapshot.running && rows.value.length === 0) return
  adopted.runId = snapshot.runId

  runId.value = snapshot.runId
  summary.value = snapshot.summary ?? null
  phase.value = snapshot.running
    ? 'running'
    : snapshot.summary?.cancelled
      ? 'cancelled'
      : 'done'
  // 接管的这一轮还在跑：它算这一页的作业，这一页的「开始」要继续拦住。
  if (snapshot.running) markRunning(mode, snapshot.runId)
  else clearRunning(mode, snapshot.runId)

  // 还在跑的一轮才重建队列；已经收工的一轮只把结果贴回**已有的行**（断线重连那一路），
  // 不会新造行、也不会动这份队列的组成。
  if (snapshot.running) applyStarted(snapshot.files ?? [])

  for (const result of snapshot.results ?? []) {
    const matched = rowForResult(result.id, result.path)
    const row = matched ?? (snapshot.running ? rowFromResult(result) : null)
    if (row === null) continue
    if (!rowAt(row.id) && !rowIndex.has(row.id)) pushRow(row)
    absorbResult(row, result)
  }
  if (snapshot.running) {
    for (const row of rows.value) {
      if (row.phase === 'queued') row.phase = 'running'
    }
  } else if (snapshot.summary?.cancelled) {
    markUnsettledCancelled()
  }
}

/**
 * 和服务端对齐一次「现在在跑什么」。
 *
 * 事件流是增量的：刷新页面会漏掉 run.started 和之前所有 file.finished，前端内存里的
 * runId 也没了，界面就退回「队列为空」—— 可 Go 侧其实还在跑，甚至已经跑完。
 * 所以每次连上事件流都先问一次现状，再接着吃增量。
 */
async function resync(): Promise<void> {
  // 三页各问一次：一页在跑不代表另外两页没有历史快照。
  const requests = PAGES.map(async (mode) => {
    try {
      return { mode, snapshot: await getCurrentRun(mode) }
    } catch {
      return null
    }
  })
  for (const item of await Promise.all(requests)) {
    if (item === null) continue
    const { mode, snapshot } = item
    // 判断必须在 withPage 里面做：pages[mode] 只在切页时落盘，当前页的 phase/runId
    // 可能还新过它。请求飞在路上时用户也可能刚点了开始——这一页正在准备/跑同一轮就别盖。
    withPage(mode, () => {
      if (phase.value === 'preparing') return
      if (runId.value === snapshot.runId && phase.value === 'running') return
      adoptSnapshot(mode, snapshot)
    })
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
  if (reconnecting.value) {
    reconnecting.value = false
    success(t('toast.reconnected'))
  }
  // 连上（含重载）就先对一次现状：漏掉的事件靠这次补齐。
  void resync()
}

export function disconnect(): void {
  if (reconnectTimer !== null) {
    window.clearTimeout(reconnectTimer)
    reconnectTimer = null
  }
  sinkActive = false
  window.__markpicEvents = undefined
  connected.value = false
  reconnecting.value = false
}
