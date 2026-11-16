/**
 * 「哪一页在跑」这一本账。
 *
 * 三页可以同时跑，可事件只带 runId：这里记着 runId 归哪一页、哪些页正忙，以及
 * 点了取消之后等确认的那只计时器。
 */
import { computed, reactive, ref } from 'vue'
import type { RemoteMode } from '../../types'
import { pages } from './page'
import { PAGES } from './types'

/**
 * 哪些页有作业在跑。
 *
 * 三页互相独立：上传、检测、下载可以各跑各的（后端也只按页限制，同一页仍然只能一轮）。
 * 切页只是把下面这组 ref 换成另一页的引用；事件按 runId 认领回它自己的页。
 */
export const runningModes = reactive<Set<RemoteMode>>(new Set())
/** runId → 这一轮属于哪一页。run.started 带 mode，之后的事件只带 runId。 */
const runModeByRunId = new Map<string, RemoteMode>()

/** 正在跑的那一轮是不是调试构建：决定「0 张」该怎么跟用户解释。 */
export const runningDebug = ref(false)

export function markRunning(mode: RemoteMode, runId: string | null): void {
  if (runId !== null && runId !== '') runModeByRunId.set(runId, mode)
  runningModes.add(mode)
}

export function clearRunning(mode: RemoteMode, runId?: string | null): void {
  runningModes.delete(mode)
  if (runId !== undefined && runId !== null) runModeByRunId.delete(runId)
}

/** 这一轮属于哪一页：先查 runId 表，再查各页自己的 runId（重连接管时补的表）。 */
export function modeOfRun(eventRunId: string): RemoteMode | null {
  const mapped = runModeByRunId.get(eventRunId)
  if (mapped !== undefined) return mapped
  for (const pageMode of PAGES) {
    if (pages[pageMode].runId === eventRunId) return pageMode
  }
  return null
}

/** 每页各自的取消确认计时器：两页同时取消时不能互相清掉。 */
export const cancelTimers = new Map<RemoteMode, number>()

/**
 * 有没有作业在跑（任何一页）。
 *
 * 顶栏、底栏用它表示「有活在干」；某一页自己的按钮则用 isRunBusy(这一页) ——
 * 下载在跑不该把上传页的「开始」也锁住。
 */
export const busy = computed(() => runningModes.size > 0)

/** 某一页有没有作业在跑：这一页的加文件 / 开始 / 清空只看自己。 */
export function isRunBusy(mode: RemoteMode): boolean {
  return runningModes.has(mode)
}

/** 正在跑的作业页，按侧边栏顺序；侧边栏据此给每个跑着的页都点一个记号。 */
export const runningPages = computed<RemoteMode[]>(() =>
  PAGES.filter((pageMode) => runningModes.has(pageMode)),
)

export function clearCancelTimer(mode?: RemoteMode): void {
  if (mode === undefined) {
    for (const timer of cancelTimers.values()) window.clearTimeout(timer)
    cancelTimers.clear()
    return
  }
  const timer = cancelTimers.get(mode)
  if (timer !== undefined) {
    window.clearTimeout(timer)
    cancelTimers.delete(mode)
  }
}
