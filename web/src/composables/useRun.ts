import { computed, markRaw, reactive, ref, shallowReactive } from 'vue'
import {
  cancelRun as requestCancelRun,
  cancelScan as requestCancelScan,
  describeError,
  getCurrentRun,
  scanStart as requestScanStart,
  startRun as requestStartRun,
} from '../api'
import type {
  CurrentRun,
  FileResult,
  ImageItem,
  PreviewResult,
  RunEvent,
  RunFileInfo,
  RunSummary,
  RemoteMode,
  ScanEvent,
  Settings,
} from '../types'
import { t } from '../i18n'
import { useHealth } from './useHealth'
import { useToasts } from './useToasts'

export type RunPhase = 'idle' | 'preparing' | 'running' | 'cancelling' | 'done' | 'cancelled'
export type RowPhase = 'queued' | 'running' | 'settled' | 'cancelled'

/**
 * 一份文档自己的扫描状态。
 *
 * 队列行一加进来就要先被扫一遍（数清有几张图、哪些是远程地址），扫完才允许开跑：
 * 图片数、远程地址清单都是跑之前就要摆到卡片上的东西。'idle' 是「还没开始扫」，
 * 每次启动恢复出来的行都是它；'cancelled' 表示用户中途掐掉了这一份，卡片上给一个
 * 「重新扫描」，而不是自动替他再扫一遍。
 */
export type ScanState = 'idle' | 'scanning' | 'ok' | 'failed' | 'timeout' | 'cancelled'

export interface RunRow {
  id: string
  path: string
  name: string
  dir: string
  images: number | null
  phase: RowPhase
  // uploaded 是「替换成功的引用数」，done 是「这一篇处理了几张图」——远程模式下只有后者会动。
  uploaded: number
  done?: number
  total: number
  /** 预览跑过之后仍然没有任何文件与之匹配（路径不存在，或目录里没有 Markdown）。 */
  missing?: boolean
  /**
   * 这一行是个目录：真正的文档要等开跑时才展开成一行一个。
   *
   * 预览是按前缀把目录里的图片数加起来的，所以能分辨出「这一行不是文档」。
   * 底栏靠它说「1 个目录」而不是「1 个文件」—— 粘一个目录进去却看到「1 个文件」，
   * 用户会以为只有一篇会被处理。
   */
  isDir?: boolean
  /**
   * 这一行的转换结果（或预检结果）。
   *
   * 结果直接挂在行上，行对象自己就是响应式的：某一行完成时只有那一行重渲染，
   * 不必重建整张列表。预检结果也用同一个字段，展开一行才有逐图明细可看。
   */
  result: FileResult | null
  /** 服务端为这次运行分配的 id（事件里的 id）。队列行的 id 由本地生成，两者都要能查到行。 */
  serverId?: string
  /** 内部记账：这份结果是否已经算进 live 统计（预检结果只贡献图片数）。 */
  scored?: boolean
  /** 这一行自己的扫描状态；只有 'ok' 的行才允许参与「开始」。 */
  scan?: ScanState
  /** 扫描没通过时给人看的一句话（超时、找不到文件、后台报错）。 */
  scanError?: string
  /**
   * 这一行的扫描属于哪一批。
   *
   * 取消之后重扫会开新的一批：上一批的收尾事件（扫完了/超时了）不能把新一批
   * 还在扫的行按超时结掉，所以每批只认自己当初点过的那几行。
   */
  scanBatch?: string
  /**
   * 正在跑的时候，这一篇逐条图片的状态。
   *
   * Go 侧不再等整篇跑完才回结果：每处理完一张就发一条增量，这里按位置覆盖。
   * 用 shallowReactive 装，换掉某一格就能让卡片上那一行重渲染，不必把上万条明细
   * 全做成响应式。跑完（file.finished）后换回整份 result，这份就丢掉。
   */
  liveItems?: ImageItem[]
  /**
   * 这一轮这一篇真正要处理多少个「单元」。
   *
   * 上传是去重后的本地图，检测/下载是去重后的远程地址 —— Go 侧的 file.progress
   * 只在处理完第一张之后才带总数，启动时先按预览明细估一份，页头那条线才能从 0
   * 开始一直按总进度走，而不是等到第一个文件完成才跳。
   */
  workTotal?: number
  /** 这一篇已经处理完的单元数；文件完成时补齐，运行中由 file.progress 推进。 */
  workDone?: number
  /**
   * 这一轮已经计进页脚「替换」的明细下标。
   *
   * 卡片每处理完一处就换状态，页脚也该跟着走；按位置去重，进度事件重放不会
   * 把数字算重。file.finished 会用结果校正一次，并清掉这份记账。
   */
  counted?: Set<number>
}

/** 列表拿到的行就带着结果。保留这个别名，免得每个组件都改一遍类型。 */
export type RunRowView = RunRow

export interface RunStats {
  files: number
  /** 队列里那些还没展开的目录行有几行（开跑之后目录展开成文件，这里回到 0）。 */
  dirs: number
  images: number
  // uploaded 是「替换了几处」，与行上的 done（处理了几张图）不是一个东西。
  uploaded: number
  /** 靠缓存省下的去重图片数（跑完之后才有准确值）。 */
  reused: number
  /** 真的发给图床几次（内容去重后可能小于 uploaded）。 */
  uploadRequests: number
  failed: number
  ok: number
  partial: number
  failedFiles: number
  noImages: number
  /** download 模式：这一轮把几张远程图下载到了本地。 */
  localized: number
  /** download 模式：去重之后真正下载了几个文件（localized 按引用处算）。 */
  localizedUnique: number
  /** check 模式：检测了几个远程地址、其中几个打不开，以及有几个文件只是被检测。 */
  remoteChecked: number
  /** 去重后真正探活过的地址数。 */
  remoteCheckedUnique: number
  remoteDead: number
  remoteDeadUnique: number
  remoteFailed: number
  remoteFailedUnique: number
  /** 碰不了的远程地址（ftp://、data: 之类）：既不算检测过，也不算失效。 */
  remoteUnsupported: number
  /** 检测/下载模式里引用了、但磁盘上找不到的本地图片总数。 */
  localMissing: number
  remoteKept: number
  filesChecked: number
  remoteReused: number
  /** 这一轮被提前掐断的原因（图床连不上之类），空串表示正常跑完或被用户取消。 */
  abortReason: string
}

const { danger, warning, success } = useToasts()

const KNOWN_EVENT_TYPES = new Set<string>([
  'run.started',
  'file.started',
  'file.progress',
  'file.finished',
  'run.finished',
  'run.cancelled',
  'log',
  'scan.file',
  'scan.done',
])

/** 运行中的统计（每页一份，切页时整份搬走，不跟着页面串味）。 */
interface LiveCounters {
  images: number
  uploaded: number
  reused: number
  uploadRequests: number
  failed: number
  ok: number
  partial: number
  failedFiles: number
  noImages: number
  localized: number
  localizedUnique: number
  remoteChecked: number
  remoteCheckedUnique: number
  remoteDead: number
  remoteDeadUnique: number
  remoteFailed: number
  remoteFailedUnique: number
  remoteUnsupported: number
  localMissing: number
  remoteKept: number
  filesChecked: number
  remoteReused: number
  abortReason: string
  settled: number
}

function emptyCounters(): LiveCounters {
  return {
    images: 0,
    uploaded: 0,
    reused: 0,
    uploadRequests: 0,
    failed: 0,
    ok: 0,
    partial: 0,
    failedFiles: 0,
    noImages: 0,
    localized: 0,
    localizedUnique: 0,
    remoteChecked: 0,
    remoteCheckedUnique: 0,
    remoteDead: 0,
    remoteDeadUnique: 0,
    remoteFailed: 0,
    remoteFailedUnique: 0,
    remoteUnsupported: 0,
    localMissing: 0,
    remoteKept: 0,
    filesChecked: 0,
    remoteReused: 0,
    abortReason: '',
    settled: 0,
  }
}

/**
 * 一个作业页的全部可变状态。
 *
 * 「转换 / 检测 / 下载」是三个独立的页面：各自的队列、各自的进度与结果，切走再切回来
 * 还是原样。后端每一页最多跑一轮，可以同时跑；每一轮属于「开始它的那一页」—— 切页只是把下面这组
 * ref 换成另一页的引用，事件照样写进跑着的那一页，不会因为画面换了就写错地方。
 */
interface QueuePage {
  rows: RunRow[]
  rowIndex: Map<string, number>
  eventIndex: Map<string, number>
  pathIndex: Map<string, number>
  phase: RunPhase
  summary: RunSummary | null
  runId: string | null
  /** 最近一次从服务端快照接管过来的 runId（挡「重连把清空过的队列又填满」）。 */
  lastAdoptedRunId: string | null
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
    lastAdoptedRunId: null,
    live: emptyCounters(),
    lastReveal: null,
    lastRevealIndex: -1,
    localSeq: 0,
    scrollTop: 0,
  }
}

const PAGES: readonly RemoteMode[] = ['ignore', 'check', 'download']
const pages: Record<RemoteMode, QueuePage> = {
  ignore: newPage(),
  check: newPage(),
  download: newPage(),
}

/** 当前显示的是哪一页。 */
let activePage: RemoteMode = 'ignore'

/**
 * 哪些页有作业在跑。
 *
 * 三页互相独立：上传、检测、下载可以各跑各的（后端也只按页限制，同一页仍然只能一轮）。
 * 切页只是把下面这组 ref 换成另一页的引用；事件按 runId 认领回它自己的页。
 */
const runningModes = reactive<Set<RemoteMode>>(new Set())
/** runId → 这一轮属于哪一页。run.started 带 mode，之后的事件只带 runId。 */
const runModeByRunId = new Map<string, RemoteMode>()

/** 正在跑的那一轮是不是调试构建：决定「0 张」该怎么跟用户解释。 */
const runningDebug = ref(false)

function markRunning(mode: RemoteMode, runId: string | null): void {
  if (runId !== null && runId !== '') runModeByRunId.set(runId, mode)
  runningModes.add(mode)
}

function clearRunning(mode: RemoteMode, runId?: string | null): void {
  runningModes.delete(mode)
  if (runId !== undefined && runId !== null) runModeByRunId.delete(runId)
}

/** 这一轮属于哪一页：先查 runId 表，再查各页自己的 runId（重连接管时补的表）。 */
function modeOfRun(eventRunId: string): RemoteMode | null {
  const mapped = runModeByRunId.get(eventRunId)
  if (mapped !== undefined) return mapped
  for (const pageMode of PAGES) {
    if (pages[pageMode].runId === eventRunId) return pageMode
  }
  return null
}

/**
 * 暂时把某一页接上来跑一段同步逻辑。
 *
 * 所有「改行、改统计」的辅助函数读的都是当前页那组 ref。事件属于别的页时，
 * 先存当前页、换上目标页，跑完再换回来 —— 同一 tick 内换出去再换回来，Vue 不会画中间态。
 */
function withPage<T>(mode: RemoteMode, fn: () => T): T {
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

const phase = ref<RunPhase>(pages[activePage].phase)
const rows = ref<RunRow[]>(pages[activePage].rows)
/**
 * id → 行下标。
 *
 * 1000 篇文档一轮会推来几千条事件，而每条事件都要按 id 找到对应行；
 * 用 `rows.findIndex` 就是每次 O(n)（1000 行 × 几千次事件 = 几百万次比较）。
 */
let rowIndex = pages[activePage].rowIndex
/** 服务端 id → 下标。事件里的 id 是服务端给的，队列行的 id 是本地的，两张表各查各的。 */
let eventIndex = pages[activePage].eventIndex
/**
 * 路径（pathKey）→ 下标。
 *
 * 兜底用：万一行没拿到 serverId（run.started 那批事件没送到、或行是先手动
 * 加进来的），file.finished 还能按路径认领已有的一行，不至于同一个文件冒出两行。
 */
let pathIndex = pages[activePage].pathIndex
const summary = ref<RunSummary | null>(pages[activePage].summary)
const runId = ref<string | null>(pages[activePage].runId)
/**
 * 最近一次从服务端快照接管过来的 runId。
 *
 * 用来挡住「一轮已经结束、重连又把它变回界面」这种情况：结束时服务端还会留一份快照，
 * 供刚刚刷新的页面接一次；但同一个页面不该靠重连把清空过的队列重新填满。
 */
let lastAdoptedRunId: string | null = pages[activePage].lastAdoptedRunId
const connected = ref(false)
const reconnecting = ref(false)

/**
 * 运行中的统计用增量维护，不再「每来一条事件就把所有行重算一遍」。
 *
 * 旧实现把统计写成 computed：任一事件动了行它就失效，然后遍历上千行、并对每份结果
 * 过滤上万条图片明细。1000 篇 × 12 张图那一轮里，这是界面卡顿（最长一帧 489ms）的主因。
 */
const live = reactive<LiveCounters>(emptyCounters())

/** 「打开输出目录」的目标：按队列顺序最后一个成功的产物。 */
let lastRevealIndex = pages[activePage].lastRevealIndex
const lastReveal = ref<string | null>(pages[activePage].lastReveal)

/** 把当前页这组引用写回 `pages`（切页、取快照之前都要先调一次）。 */
function savePage(): void {
  const page = pages[activePage]
  page.rows = rows.value
  page.rowIndex = rowIndex
  page.eventIndex = eventIndex
  page.pathIndex = pathIndex
  page.phase = phase.value
  page.summary = summary.value
  page.runId = runId.value
  page.lastAdoptedRunId = lastAdoptedRunId
  page.live = { ...live }
  page.lastReveal = lastReveal.value
  page.lastRevealIndex = lastRevealIndex
  page.localSeq = localSeq
}

/** 把某一页接上来（不保存当前页 —— 调用方负责先 `savePage()`）。 */
function loadPage(mode: RemoteMode): void {
  activePage = mode
  const page = pages[mode]
  rows.value = page.rows
  rowIndex = page.rowIndex
  eventIndex = page.eventIndex
  pathIndex = page.pathIndex
  phase.value = page.phase
  summary.value = page.summary
  runId.value = page.runId
  lastAdoptedRunId = page.lastAdoptedRunId
  lastReveal.value = page.lastReveal
  lastRevealIndex = page.lastRevealIndex
  localSeq = page.localSeq
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

/** 三页各自的路径清单（持久化用）；调用前会把当前页写回 pages。 */
export function queuePagePaths(): Record<RemoteMode, string[]> {
  savePage()
  const out = {} as Record<RemoteMode, string[]>
  for (const mode of PAGES) out[mode] = pages[mode].rows.map((row) => row.path)
  return out
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

/**
 * 把上次记着的路径接回某一页（启动时用）。
 *
 * 只造行、不预检 —— 预检要发网络/磁盘请求，交给调用方按页去做。返回真正新增的路径。
 */
export function restoreQueuePage(mode: RemoteMode, saved: readonly string[]): string[] {
  if (mode === activePage) savePage()
  const page = pages[mode]
  if (page.rows.length > 0 || page.phase !== 'idle') return []

  const known = new Set<string>()
  const added: string[] = []
  for (const raw of saved) {
    const path = raw.trim().replace(/^"(.*)"$/, '$1').trim()
    if (path === '') continue
    const key = pathKey(path)
    if (key === '' || known.has(key)) continue
    known.add(key)
    added.push(path)
    const { name, dir } = splitPath(path)
    page.rows.push({
      id: `local-${++page.localSeq}`,
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
  page.rowIndex = new Map(page.rows.map((row, index) => [row.id, index]))
  if (mode === activePage) {
    rows.value = page.rows
    rowIndex = page.rowIndex
    localSeq = page.localSeq
  }
  return added
}

let sinkActive = false
let reconnectTimer: number | null = null
// 一轮只播报一次结局：服务端在「自己掐掉」时会先发 run.cancelled 再发 run.finished，
// 两条都带 summary.abortReason，不拦一下就会弹出两条一模一样的气泡。
const announcedRuns = new Set<string>()

/** 一轮在后台跑完时让任务栏闪一下：人切去干别的了，总得有个机会注意到。 */
function flashIfAway(): void {
  if (document.hasFocus()) return
  const bridge = window as unknown as { cmpWinFlash?: () => Promise<unknown> }
  void bridge.cmpWinFlash?.()
}
/** 每页各自的取消确认计时器：两页同时取消时不能互相清掉。 */
const cancelTimers = new Map<RemoteMode, number>()
let localSeq = 0

/**
 * 有没有作业在跑（任何一页）。
 *
 * 顶栏、底栏用它表示「有活在干」；某一页自己的按钮则用 isRunBusy(这一页) ——
 * 下载在跑不该把上传页的「开始」也锁住。
 */
const busy = computed(() => runningModes.size > 0)

/** 某一页有没有作业在跑：这一页的加文件 / 开始 / 清空只看自己。 */
function isRunBusy(mode: RemoteMode): boolean {
  return runningModes.has(mode)
}

/** 正在跑的作业页，按侧边栏顺序；侧边栏据此给每个跑着的页都点一个记号。 */
const runningPages = computed<RemoteMode[]>(() =>
  PAGES.filter((pageMode) => runningModes.has(pageMode)),
)

/**
 * 传给列表的行数组。
 *
 * 这里只做一次浅拷贝（数组元素还是那些行对象，不复制行）：所以它只在队列结构变化
 * （增行、删行、开始一轮）时才重新计算，行内属性的变化由每一行自己触发重渲染。
 */
const rowViews = computed<RunRowView[]>(() => rows.value.slice())

const totalCount = computed(() => rows.value.length)

const completedCount = computed(() => live.settled)

const stats = computed<RunStats>(() => {
  const finished = summary.value
  if (finished) {
    return {
      files: finished.files,
      // 跑完之后目录已经展开成一个个文件了，这里不再有「目录」这回事。
      dirs: 0,
      images: finished.images,
      uploaded: finished.uploaded,
      reused: finished.reused,
      uploadRequests: finished.uploadRequests ?? 0,
      failed: finished.failedImages,
      ok: finished.ok,
      partial: finished.partial,
      failedFiles: finished.failed,
      noImages: finished.noImages,
      localized: finished.localized ?? 0,
      localizedUnique: finished.localizedUnique ?? 0,
      remoteChecked: finished.remoteChecked ?? 0,
      remoteCheckedUnique: finished.remoteCheckedUnique ?? 0,
      remoteDead: finished.remoteDead ?? 0,
      remoteDeadUnique: finished.remoteDeadUnique ?? 0,
      remoteFailed: finished.remoteFailed ?? 0,
      remoteFailedUnique: finished.remoteFailedUnique ?? 0,
      remoteUnsupported: finished.remoteUnsupported ?? 0,
      localMissing: finished.localMissing ?? 0,
      remoteKept: finished.remoteKept ?? 0,
      filesChecked: finished.filesChecked ?? 0,
      remoteReused: finished.remoteReused ?? 0,
      abortReason: finished.abortReason ?? '',
    }
  }

  return {
    files: rows.value.length,
    dirs: rows.value.reduce((n, row) => n + (row.isDir ? 1 : 0), 0),
    images: live.images,
    uploaded: live.uploaded,
    reused: live.reused,
    uploadRequests: live.uploadRequests,
    failed: live.failed,
    ok: live.ok,
    partial: live.partial,
    failedFiles: live.failedFiles,
    noImages: live.noImages,
    localized: live.localized,
    localizedUnique: live.localizedUnique,
    remoteChecked: live.remoteChecked,
    remoteCheckedUnique: live.remoteCheckedUnique,
    remoteDead: live.remoteDead,
    remoteDeadUnique: live.remoteDeadUnique,
    remoteFailed: live.remoteFailed,
    remoteFailedUnique: live.remoteFailedUnique,
    remoteUnsupported: live.remoteUnsupported,
    localMissing: live.localMissing,
    remoteKept: live.remoteKept,
    filesChecked: live.filesChecked,
    remoteReused: live.remoteReused,
    abortReason: live.abortReason,
  }
})

const revealTarget = computed<string | null>(() => {
  if (lastReveal.value !== null) return lastReveal.value
  // 只检测的那一轮一个文件都没写：这时候还挂着「打开输出目录」，点开的是源目录，
  // 名字和行为对不上（用户会以为转换结果就放在那儿）。
  const now = stats.value
  if (now.filesChecked > 0 && now.uploaded === 0 && now.localized === 0) return null
  return rows.value[0]?.dir ?? null
})

function pathKey(value: string): string {
  return value.replace(/\//g, '\\').replace(/\\+$/, '').toLowerCase()
}

function splitPath(value: string): { name: string; dir: string } {
  const trimmed = value.trim().replace(/\//g, '\\')
  const index = trimmed.lastIndexOf('\\')
  if (index > 0) {
    return { name: trimmed.slice(index + 1), dir: trimmed.slice(0, index) }
  }
  return { name: trimmed, dir: '' }
}

function clearCancelTimer(mode?: RemoteMode): void {
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

function isRunEvent(value: unknown): value is RunEvent {
  if (typeof value !== 'object' || value === null) return false
  const record = value as Record<string, unknown>
  const type = record.type
  if (typeof type !== 'string' || !KNOWN_EVENT_TYPES.has(type)) return false
  if (type === 'log') {
    return typeof record.level === 'string' && typeof record.message === 'string'
  }
  return typeof record.runId === 'string'
}

function matches(eventRunId: string): boolean {
  return runId.value !== null && runId.value === eventRunId
}

function liveReset(): void {
  live.images = 0
  live.uploaded = 0
  live.reused = 0
  live.uploadRequests = 0
  live.failed = 0
  live.ok = 0
  live.partial = 0
  live.failedFiles = 0
  live.noImages = 0
  live.localized = 0
  live.localizedUnique = 0
  live.remoteChecked = 0
  live.remoteCheckedUnique = 0
  live.remoteDead = 0
  live.remoteDeadUnique = 0
  live.remoteFailed = 0
  live.remoteFailedUnique = 0
  live.remoteUnsupported = 0
  live.localMissing = 0
  live.remoteKept = 0
  live.filesChecked = 0
  live.settled = 0
  live.abortReason = ''
  lastReveal.value = null
  lastRevealIndex = -1
}

/** 队列刚建好时，用每行预估的图片数垫上总数，别让页脚在跑之前显示「0 张图」。 */
function seedImagesFromRows(): void {
  let images = 0
  for (const row of rows.value) images += row.images ?? row.total
  live.images = images
}

/** 远程地址的判定跟 Go 侧 isRemoteURL 一致：http/https/协议相对。 */
function isRemoteTarget(target: string): boolean {
  const t = target.trim().toLowerCase()
  // ftp:// 与 data: 这类我们碰不了，Go 不会把它们列成单元，这里也不能算进分母。
  return t.startsWith('http://') || t.startsWith('https://') || t.startsWith('//')
}

/**
 * 从一份明细里数这一轮真正要处理的单元。
 *
 * 上传按本地绝对路径去重（同一张图引用多次只传一次）；检测/下载按远程地址去重
 * （同一个地址只探一次）。和 Go 侧 runUnits 的口径一致，所以预览估出来的总数
 * 能直接当分母，第一个 file.progress 到了之后再换成服务端的权威值。
 */
function workUnitsIn(result: FileResult | null | undefined, mode: RemoteMode): number {
  const items = result?.items
  if (!items || items.length === 0) return 0
  const seen = new Set<string>()
  for (const item of items) {
    if (mode === 'ignore') {
      // 本地图只有真的存在才会进 groups；absolutePath 为空的是缺失引用，不算单元。
      if (item.absolutePath === '') continue
      seen.add(item.absolutePath.toLowerCase())
      continue
    }
    if (!isRemoteTarget(item.target)) continue
    seen.add(item.target.trim())
  }
  return seen.size
}

function reusedInResult(result: FileResult): number {
  // 服务端会在结果里直接给出复用张数；老版本没有这个字段时就退回自己数明细。
  if (typeof result.reused === 'number') return result.reused
  return result.items.filter(item => item.status === 'reused' || item.status === 'cached').length
}

function scoreStatus(status: FileResult['status'], sign: number): void {
  if (status === 'ok') live.ok += sign
  else if (status === 'partial') live.partial += sign
  else if (status === 'failed') live.failedFiles += sign
  else if (status === 'no-images') live.noImages += sign
  else if (status === 'checked') live.filesChecked += sign
}

function rowAt(id: string): RunRow | null {
  const index = eventIndex.get(id) ?? rowIndex.get(id)
  if (index === undefined) return null
  return rows.value[index] ?? null
}

function reindexRows(): void {
  rowIndex.clear()
  eventIndex.clear()
  pathIndex.clear()
  rows.value.forEach((row, index) => {
    rowIndex.set(row.id, index)
    if (row.serverId) eventIndex.set(row.serverId, index)
    pathIndex.set(pathKey(row.path), index)
  })
}

function pushRow(row: RunRow): void {
  rowIndex.set(row.id, rows.value.length)
  if (row.serverId) eventIndex.set(row.serverId, rows.value.length)
  pathIndex.set(pathKey(row.path), rows.value.length)
  rows.value.push(row)
}

/**
 * 找到 file.finished 该落到哪一行。
 *
 * 先按服务端 id 找；找不到再按路径找（事件合并、漏收 run.started 时的兜底）。
 */
function rowForResult(id: string, path: string): RunRow | null {
  const byId = rowAt(id)
  if (byId) return byId
  const index = pathIndex.get(pathKey(path))
  if (index === undefined) return null
  return rows.value[index] ?? null
}

function updateRow(
  id: string,
  patch: Partial<Pick<RunRow, 'phase' | 'uploaded' | 'total' | 'images'>>,
): void {
  const row = rowAt(id)
  if (!row) return
  Object.assign(row, patch)
}

function rowFromResult(result: FileResult): RunRow {
  return {
    id: result.id,
    path: result.path,
    name: result.name,
    dir: result.dir,
    images: result.images,
    phase: 'queued',
    uploaded: 0,
    total: result.images,
    result: null,
    // 服务端能给出一份结果，说明这一份早就读通了 —— 它不该再挡住「开始」。
    scan: 'ok',
  }
}

/**
 * 把一份文件结果记到行上，并把增量写进 live 统计。
 *
 * 「图片总数」这一项要把该行原先的贡献换掉（排队时是预估、预检时是预检结果、
 * 完成后是真实结果），所以先减旧值再加新值。
 */
function absorbResult(row: RunRow, result: FileResult): void {
  if (row.scored && row.result) {
    live.images -= row.result.images
    live.reused -= reusedInResult(row.result)
    live.failed -= row.result.failed
    live.localized -= row.result.localized ?? 0
    live.localizedUnique -= row.result.localizedUnique ?? 0
    live.remoteChecked -= row.result.checked ?? 0
    live.remoteCheckedUnique -= row.result.checkedUnique ?? 0
    live.remoteDead -= row.result.dead ?? 0
    live.remoteDeadUnique -= row.result.deadUnique ?? 0
    live.remoteFailed -= row.result.remoteFailed ?? 0
    live.remoteFailedUnique -= row.result.remoteFailedUnique ?? 0
    live.remoteUnsupported -= row.result.remoteUnsupported ?? 0
    live.localMissing -= row.result.localMissing ?? 0
  live.remoteKept -= row.result.remoteKept ?? 0
    scoreStatus(row.result.status, -1)
  } else {
    live.images -= row.images ?? row.total
  }

  live.images += result.images
  live.uploaded += result.uploaded - row.uploaded
  live.reused += reusedInResult(result)
  live.failed += result.failed
  live.localized += result.localized ?? 0
  live.localizedUnique += result.localizedUnique ?? 0
  live.remoteChecked += result.checked ?? 0
  live.remoteCheckedUnique += result.checkedUnique ?? 0
  live.remoteDead += result.dead ?? 0
  live.remoteDeadUnique += result.deadUnique ?? 0
  live.remoteFailed += result.remoteFailed ?? 0
  live.remoteFailedUnique += result.remoteFailedUnique ?? 0
  live.remoteUnsupported += result.remoteUnsupported ?? 0
  live.localMissing += result.localMissing ?? 0
  live.remoteKept += result.remoteKept ?? 0
  // 这一篇的活干完了：用结果里的明细重新数一遍单元。第一个 file.progress 之前
  // 的分母是按预览估算的，比真实值大的话会一直把总进度压低；一个单元都没有
  // （纯本地图/纯远程图）就该从分母里消失，不能把进度条永远钉在中间。
  row.workTotal = workUnitsIn(result, activePage)
  row.workDone = row.workTotal
  scoreStatus(result.status, 1)
  if (!row.scored && row.phase !== 'cancelled') live.settled += 1

  const index = rowIndex.get(row.id) ?? (row.serverId ? eventIndex.get(row.serverId) : undefined) ?? -1
  row.result = markRaw(result)
  row.liveItems = undefined
  row.counted = undefined
  row.scored = true
  row.phase = 'settled'
  row.uploaded = result.uploaded
  row.total = result.images
  row.images = result.images

  if (
    index > lastRevealIndex &&
    (result.status === 'ok' || result.status === 'partial') &&
    result.outputPath
  ) {
    lastRevealIndex = index
    lastReveal.value = result.outputPath
  }
}

function applyStarted(files: RunFileInfo[]): void {
  // 后端只会回传真正找到的文件。用户加进来、但一个文件都没匹配上的路径要留在队列里，
  // 否则一点「开始转换」它就凭空消失，用户无从知道这条路径被忽略了。
  //
  // 匹配上的行按路径复用同一个对象（只改字段），组件 key（row.id）因此保持不变：
  // 1000 行不会因为「本地 id 换成服务端 id」而整批卸载重挂，展开的明细也不会被收起。
  const byPath = new Map<string, RunRow>()
  for (const row of rows.value) {
    const key = pathKey(row.path)
    if (!byPath.has(key)) byPath.set(key, row)
  }

  const known = new Set(files.map(file => pathKey(file.path)))
  const used = new Set<RunRow>()
  const started: RunRow[] = files.map(file => {
    const existing = byPath.get(pathKey(file.path))
    if (existing && !used.has(existing)) {
      used.add(existing)
      existing.serverId = file.id
      existing.path = file.path
      existing.name = file.name
      existing.dir = file.dir
      existing.images = file.images
      existing.phase = 'queued'
      existing.uploaded = 0
      existing.total = file.images
      existing.missing = false
      // 上一轮的明细先留着：这一轮每跑完一个文件，事件会把结果换掉。
      // 立刻置空会让检测页/下载页在跑的过程中变成空白页（看着像崩了）。
      existing.scored = false
      existing.scan = 'ok'
      existing.scanError = undefined
      existing.scanBatch = undefined
      existing.liveItems = undefined
      existing.counted = undefined
      return existing
    }
    return {
      id: file.id,
      serverId: file.id,
      path: file.path,
      name: file.name,
      dir: file.dir,
      images: file.images,
      phase: 'queued',
      uploaded: 0,
      total: file.images,
      result: null,
      scan: 'ok',
    }
  })
  const kept = rows.value
    .filter(row => row.missing && !known.has(pathKey(row.path)))
    .map(row => ({
      ...row,
      serverId: undefined,
      phase: 'queued' as const,
      uploaded: 0,
      total: row.images ?? 0,
      result: null,
      scored: false,
      liveItems: undefined,
    }))

  rows.value = [...started, ...kept]
  // 服务端的 run.started 只带「这一篇几张图」，没带本模式的去重单元数；
  // 先用预览明细估一份，file.progress 一来就会被权威值覆盖。
  for (const row of rows.value) {
    row.workTotal = workUnitsIn(row.result, activePage)
    row.workDone = 0
  }
  reindexRows()
  liveReset()
  seedImagesFromRows()
}

/** 取消的那句提示按页说：站在「检测」上却说「转换已取消」很出戏。 */
function cancelText(mode: RemoteMode): string {
  if (mode === 'check') return t('toast.runCancelled.check')
  if (mode === 'download') return t('toast.runCancelled.download')
  return t('toast.runCancelled')
}

function markUnsettledCancelled(): void {
  for (const row of rows.value) {
    if (row.phase !== 'settled') row.phase = 'cancelled'
  }
  live.settled = rows.value.length
}

/**
 * 给一行准备「运行中的逐条明细」。
 *
 * 预览时已经把整份 items 摆出来了，先照抄一份；第一张图处理完之后，Go 侧每条进度
 * 只说「第几处变成了什么」，这里按位置盖上去。照抄的一定要是浅响应数组：整份深响应
 * 在几万条明细的文档上会把界面拖住。
 */
function seedLiveItems(row: RunRow): void {
  if (!row.liveItems) row.liveItems = shallowReactive([...(row.result?.items ?? [])])
}

/**
 * 把一条事件送回它自己那一页。
 *
 * 三页可以同时在跑，事件只带 runId；run.started 带 mode，先把 runId 记到那一页，
 * 之后这条 runId 的所有事件都认领回同一页 —— 哪怕用户正看着别的页。
 */
function onSignal(event: RunEvent): void {
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

function applyRunEvent(event: RunEvent, owner: RemoteMode): void {
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
const EVENT_FLUSH_MS = 16

/**
 * 一次最多应用多少条事件。
 *
 * 服务端是按批下发的，一批可能有上千条。一次全应用会让 Vue 在同一个 tick 里
 * 更新上千个组件、浏览器紧接着同步重排整张表——5000 篇那次实测出现过 5.7 秒的
 * 单帧停顿。分片之后每帧的工作量有上限，剩下的下一帧接着做。
 */
const EVENT_FLUSH_MAX = 200

const pending: RunEvent[] = []
let pendingTimer: number | null = null

function flushPending(): void {
  pendingTimer = null
  if (pending.length === 0) return
  const batch = pending.splice(0, EVENT_FLUSH_MAX)
  for (const event of batch) onSignal(event)
  if (pending.length > 0) {
    pendingTimer = window.setTimeout(flushPending, EVENT_FLUSH_MS)
  }
}

function enqueue(event: RunEvent): void {
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
function adoptSnapshot(mode: RemoteMode, snapshot: CurrentRun): void {
  if (!snapshot.runId) return
  // 这一轮的结果已经在界面上了（自己刚跑完），不要用快照再盖一遍。
  if (summary.value !== null && summary.value.runId === snapshot.runId) return
  // 已经结束、而且这个页面早就见过它（比如用户跑完又清空了队列）：
  // 重连时别把旧结果重新变出来。
  if (!snapshot.running && lastAdoptedRunId === snapshot.runId) return
  // 上一次会话已经收工的一轮不进队列：页面上还没有行时接管它，等于把那一轮的文件
  // 当成这一页的队列（还会把 localStorage 里别的页的路径覆盖掉）。
  if (!snapshot.running && rows.value.length === 0) return
  lastAdoptedRunId = snapshot.runId

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

function connect(): void {
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

function disconnect(): void {
  if (reconnectTimer !== null) {
    window.clearTimeout(reconnectTimer)
    reconnectTimer = null
  }
  sinkActive = false
  window.__markpicEvents = undefined
  connected.value = false
  reconnecting.value = false
}

/** 把每一行恢复到「还没跑」的样子（行对象原地改，不重建数组）。 */
function resetRowsForRun(): void {
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

function resetOutcome(): void {
  phase.value = 'idle'
  summary.value = null
  runId.value = null
  resetRowsForRun()
}

/** 加入队列；返回真正新增的路径，供调用方做预览。 */
function addPaths(input: string[]): string[] {
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
      id: `local-${++localSeq}`,
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

/* ---------------- 扫描（每张卡片自己扫） ---------------- */

interface ScanBatch {
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
const scanBatches = new Map<string, ScanBatch>()
let scanSeq = 0

/**
 * 整批扫描的兜底时限。
 *
 * Go 侧每个文件 20 秒封顶，这里再宽一点。之所以还要一层：后台进程可能压根没接上
 * 窗口（刚启动、WebView 还在加载），事件一条都送不到，界面不能因此永远点不了「开始」。
 */
const SCAN_BATCH_TIMEOUT_MS = 26_000

/** 取某一页的行数组。当前页先写回 pages，保证读到的就是界面上的那批对象。 */
function pageRows(mode: RemoteMode): RunRow[] {
  if (mode === activePage) savePage()
  return pages[mode].rows
}

function rowOfPage(mode: RemoteMode, path: string): RunRow | undefined {
  const key = pathKey(path)
  return pageRows(mode).find((row) => pathKey(row.path) === key)
}

/**
 * 把一份文件（或一个目录下的若干文件）的扫描结果写到行上。
 *
 * 目录行没有「自己」的结果 —— 前缀命中的那些加起来就是它的张数，和以前
 * preview 的做法一致；一份都没命中说明路径不存在或目录里没有 Markdown。
 */
function writeScanResult(row: RunRow, files: readonly FileResult[]): void {
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
function finishBatch(batch: ScanBatch, state: ScanState, message?: string): void {
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
function onScanEvent(event: ScanEvent): void {
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
function requestBatchCancel(batch: ScanBatch, path?: string): void {
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

async function start(paths: string[], settings: Settings): Promise<boolean> {
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

async function cancel(): Promise<void> {
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

function removeRow(id: string): void {
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

function clearQueue(): void {
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
    revealTarget,
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
