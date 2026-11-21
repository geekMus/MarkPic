/**
 * 三个作业页的一切：队列、扫描、事件、按钮。
 *
 * 这一层是门面 —— 组件只认 `composables/useRun`，不关心内部拆成了几块。`useRun()` 返回的
 * 东西与拆分前逐字一致；除此以外，早先零星从这里导入的函数（页面切换、扫描重试等）也原样
 * 再导出，所以调用方一行都不用改。
 */
import { busy, isRunBusy, runningPages } from './running'
import { completedCount, rowViews, scanCardCount, stats, taskRows, totalCount } from './stats'
import { cancel, clearQueue, addPaths, removeRow, start } from './commands'
import { connect, disconnect } from './events'
import { phase, rows, summary } from './page'
import { cancelRowScan, retryRowScan, scanPaths } from './scan'

export * from './types'
export * from './paths'
export * from './running'
export * from './stats'
export * from './page'
export * from './rows'
export * from './scan'
export * from './events'
export * from './commands'

/** 组件拿到的这一份：一个是界面要的响应式状态与总数，另一个是用户按下去的动作。 */
export function useRun() {
  return {
    phase,
    rows,
    rowViews,
    stats,
    summary,
    busy,
    runningPages,
    isRunBusy,
    totalCount,
    completedCount,
    taskRows,
    scanCardCount,
    connect,
    disconnect,
    addPaths,
    scanPaths,
    cancelRowScan,
    retryRowScan,
    start,
    cancel,
    removeRow,
    clearQueue,
  }
}
