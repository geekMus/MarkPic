/**
 * 三个作业页各自的那一份状态，以及「当前在看哪一页」。
 *
 * 转换 / 检测 / 下载互不干扰：每页有自己的队列、进度与结果，切走再切回还是原样；后端每页
 * 最多跑一轮，几页可以同时跑。下面这组 ref 只是当前页的投影：换页时先把投影写回 pages，
 * 再把目标页接上来（savePage / loadPage）—— 一张桌子，三份抽屉。
 *
 * 除 ref 之外还有几样普通值（三张索引表、接管过的 runId、末次产物下标、本地 id 计数器）：
 * 它们只在换页时整体搬动，做成响应式是白花钱。ES 模块的 import 不能重新赋值，所以要被
 * 别的模块改的那几样装在对象里（adopted / revealIndex / seq），改的是字段。
 */
import { reactive, ref } from 'vue'
import type { RemoteMode, RunSummary } from '../../types'
import type { LiveCounters, RunPhase, RunRow } from './types'
import { emptyCounters } from './types'

/**
 * 一个作业页的全部可变状态。
 *
 * 「转换 / 检测 / 下载」是三个独立的页面：各自的队列、各自的进度与结果，切走再切回来
 * 还是原样。后端每一页最多跑一轮，可以同时跑；每一轮属于「开始它的那一页」—— 切页只是把下面这组
 * ref 换成另一页的引用，事件照样写进跑着的那一页，不会因为画面换了就写错地方。
 */
export interface QueuePage {
  rows: RunRow[]
  rowIndex: Map<string, number>
  eventIndex: Map<string, number>
  pathIndex: Map<string, number>
  phase: RunPhase
  summary: RunSummary | null
  runId: string | null
  live: LiveCounters
  lastReveal: string | null
  lastRevealIndex: number
  localSeq: number
  /** 这一页列表的滚动位置：切回来接着看上次那一屏。 */
  scrollTop: number
}

function newPage(): QueuePage {
  return {
    rows: [],
    rowIndex: new Map<string, number>(),
    eventIndex: new Map<string, number>(),
    pathIndex: new Map<string, number>(),
    phase: 'idle',
    summary: null,
    runId: null,
    live: emptyCounters(),
    lastReveal: null,
    lastRevealIndex: -1,
    localSeq: 0,
    scrollTop: 0,
  }
}

export const pages: Record<RemoteMode, QueuePage> = {
  ignore: newPage(),
  check: newPage(),
  download: newPage(),
}

/** 当前显示的是哪一页。 */
export let activePage: RemoteMode = 'ignore'

/**
 * 暂时把某一页接上来跑一段同步逻辑。
 *
 * 所有「改行、改统计」的辅助函数读的都是当前页那组 ref。事件属于别的页时，
 * 先存当前页、换上目标页，跑完再换回来 —— 同一 tick 内换出去再换回来，Vue 不会画中间态。
 */
export function withPage<T>(mode: RemoteMode, fn: () => T): T {
  if (mode === activePage) return fn()
  const original = activePage
  savePage()
  loadPage(mode)
  try {
    return fn()
  } finally {
    savePage()
    loadPage(original)
  }
}

export const phase = ref<RunPhase>(pages[activePage].phase)
export const rows = ref<RunRow[]>(pages[activePage].rows)
/**
 * id → 行下标。
 *
 * 1000 篇文档一轮会推来几千条事件，而每条事件都要按 id 找到对应行；
 * 用 `rows.findIndex` 就是每次 O(n)（1000 行 × 几千次事件 = 几百万次比较）。
 */
export let rowIndex = pages[activePage].rowIndex
/** 服务端 id → 下标。事件里的 id 是服务端给的，队列行的 id 是本地的，两张表各查各的。 */
export let eventIndex = pages[activePage].eventIndex
/**
 * 路径（pathKey）→ 下标。
 *
 * 兜底用：万一行没拿到 serverId（run.started 那批事件没送到、或行是先手动
 * 加进来的），file.finished 还能按路径认领已有的一行，不至于同一个文件冒出两行。
 */
export let pathIndex = pages[activePage].pathIndex
export const summary = ref<RunSummary | null>(pages[activePage].summary)
export const runId = ref<string | null>(pages[activePage].runId)
/**
 * 运行中的统计用增量维护，不再「每来一条事件就把所有行重算一遍」。
 *
 * 旧实现把统计写成 computed：任一事件动了行它就失效，然后遍历上千行、并对每份结果
 * 过滤上万条图片明细。1000 篇 × 12 张图那一轮里，这是界面卡顿（最长一帧 489ms）的主因。
 */
export const live = reactive<LiveCounters>(emptyCounters())

/** 「打开输出目录」的目标：按队列顺序最后一个成功的产物。 */
export const revealIndex = { value: pages[activePage].lastRevealIndex }
/** 本地 id 的计数器：换页时跟着那一页走（每一页存的是自己那份）。 */
const seq = { local: 0 }
export const lastReveal = ref<string | null>(pages[activePage].lastReveal)

/** 把当前页这组引用写回 `pages`（切页、取快照之前都要先调一次）。 */
export function savePage(): void {
  const page = pages[activePage]
  page.rows = rows.value
  page.rowIndex = rowIndex
  page.eventIndex = eventIndex
  page.pathIndex = pathIndex
  page.phase = phase.value
  page.summary = summary.value
  page.runId = runId.value
  page.live = { ...live }
  page.lastReveal = lastReveal.value
  page.lastRevealIndex = revealIndex.value
  page.localSeq = seq.local
}

/** 把某一页接上来（不保存当前页 —— 调用方负责先 `savePage()`）。 */
export function loadPage(mode: RemoteMode): void {
  activePage = mode
  const page = pages[mode]
  rows.value = page.rows
  rowIndex = page.rowIndex
  eventIndex = page.eventIndex
  pathIndex = page.pathIndex
  phase.value = page.phase
  summary.value = page.summary
  runId.value = page.runId
  lastReveal.value = page.lastReveal
  revealIndex.value = page.lastRevealIndex
  seq.local = page.localSeq
  Object.assign(live, emptyCounters(), page.live)
}

/**
 * 切换作业页：把当前页存回去，再把目标页接上来。
 *
 * 导航栏点到别的作业菜单、或者从表单页回到某个作业页时调用。跑着的那一轮不在
 * 这里换归属 —— 进度留在它自己那一页上。
 */
export function setQueuePage(mode: RemoteMode): void {
  if (mode === activePage) return
  savePage()
  loadPage(mode)
}

/** 当前显示的是哪一页（`ignore` = 转换）。 */
export function currentQueuePage(): RemoteMode {
  return activePage
}

/** 这一页上次滚到哪儿了。 */
/** 这一页里还没预检过的行（`images === null` 的那批）：切到这一页时再扫。 */
export function queuePagePendingPreview(mode: RemoteMode): string[] {
  if (mode === activePage) savePage()
  // 只扫「压根没扫过」的行：被用户取消、扫超时、找不到文件的行也在等张数，
  // 但它们的状态是给人看的 —— 自动重扫会把「已取消扫描」这个提示冲掉。
  // 那几种要用户自己点「恢复扫描」/「重试」。
  return pages[mode].rows
    .filter((row) => row.images === null && (row.scan ?? 'idle') === 'idle')
    .map((row) => row.path)
}

export function queuePageScroll(mode: RemoteMode): number {
  return pages[mode].scrollTop
}

/** 记下这一页的滚动位置（切页/卸载前调）。 */
export function rememberQueueScroll(mode: RemoteMode, top: number): void {
  pages[mode].scrollTop = top
}

/** 取某一页的行数组。当前页先写回 pages，保证读到的就是界面上的那批对象。 */
export function pageRows(mode: RemoteMode): RunRow[] {
  if (mode === activePage) savePage()
  return pages[mode].rows
}

/**
 * 给某一页的新行发一个本地 id（服务端那套 id 要等扫描回来才有）。
 *
 * 计数器必须按页唯一：seq.local 只是「当前页那个计数器」的别名，切页时同步。
 * 以前扫描展开直接 ++page.localSeq、而 addPaths 走 seq.local —— 两套计数器各走各的，
 * 同一页会发出重复的 id；Vue 按 key 复用组件，于是出现「卡片标题和展开内容对不上」。
 */
export function pageLocalId(mode: RemoteMode): string {
  const page = pages[mode]
  page.localSeq += 1
  if (mode === activePage) seq.local = page.localSeq
  return 'local-' + page.localSeq
}

export function nextLocalId(): string {
  return pageLocalId(activePage)
}