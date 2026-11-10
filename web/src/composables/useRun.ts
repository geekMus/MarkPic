import { computed, markRaw, reactive, ref } from 'vue'
import {
  cancelRun as requestCancelRun,
  describeError,
  getCurrentRun,
  startRun as requestStartRun,
} from '../api'
import type {
  CurrentRun,
  FileResult,
  PreviewResult,
  RunEvent,
  RunFileInfo,
  RunSummary,
  Settings,
} from '../types'
import { t } from '../i18n'
import { useToasts } from './useToasts'

export type RunPhase = 'idle' | 'preparing' | 'running' | 'cancelling' | 'done' | 'cancelled'
export type RowPhase = 'queued' | 'running' | 'settled' | 'cancelled'

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
])

const phase = ref<RunPhase>('idle')
const rows = ref<RunRow[]>([])
/**
 * id → 行下标。
 *
 * 1000 篇文档一轮会推来几千条事件，而每条事件都要按 id 找到对应行；
 * 用 `rows.findIndex` 就是每次 O(n)（1000 行 × 几千次事件 = 几百万次比较）。
 */
const rowIndex = new Map<string, number>()
/** 服务端 id → 下标。事件里的 id 是服务端给的，队列行的 id 是本地的，两张表各查各的。 */
const eventIndex = new Map<string, number>()
/**
 * 路径（pathKey）→ 下标。
 *
 * 兜底用：万一行没拿到 serverId（run.started 那批事件没送到、或行是先手动
 * 加进来的），file.finished 还能按路径认领已有的一行，不至于同一个文件冒出两行。
 */
const pathIndex = new Map<string, number>()
const summary = ref<RunSummary | null>(null)
const runId = ref<string | null>(null)
/**
 * 最近一次从服务端快照接管过来的 runId。
 *
 * 用来挡住「一轮已经结束、重连又把它变回界面」这种情况：结束时服务端还会留一份快照，
 * 供刚刚刷新的页面接一次；但同一个页面不该靠重连把清空过的队列重新填满。
 */
let lastAdoptedRunId: string | null = null
const connected = ref(false)
const reconnecting = ref(false)

/**
 * 运行中的统计用增量维护，不再「每来一条事件就把所有行重算一遍」。
 *
 * 旧实现把统计写成 computed：任一事件动了行它就失效，然后遍历上千行、并对每份结果
 * 过滤上万条图片明细。1000 篇 × 12 张图那一轮里，这是界面卡顿（最长一帧 489ms）的主因。
 */
const live = reactive({
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
})

/** 「打开输出目录」的目标：按队列顺序最后一个成功的产物。 */
let lastRevealIndex = -1
const lastReveal = ref<string | null>(null)

let sinkActive = false
let reconnectTimer: number | null = null
// 一轮只播报一次结局：服务端在「自己掐掉」时会先发 run.cancelled 再发 run.finished，
// 两条都带 summary.abortReason，不拦一下就会弹出两条一模一样的气泡。
let outcomeAnnounced = false
let cancelTimer: number | null = null
let localSeq = 0

const busy = computed(
  () => phase.value === 'preparing' || phase.value === 'running' || phase.value === 'cancelling',
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

function clearCancelTimer(): void {
  if (cancelTimer !== null) {
    window.clearTimeout(cancelTimer)
    cancelTimer = null
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
  scoreStatus(result.status, 1)
  if (!row.scored && row.phase !== 'cancelled') live.settled += 1

  const index = rowIndex.get(row.id) ?? (row.serverId ? eventIndex.get(row.serverId) : undefined) ?? -1
  row.result = markRaw(result)
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
      existing.result = null
      existing.scored = false
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
    }))

  rows.value = [...started, ...kept]
  reindexRows()
  liveReset()
  seedImagesFromRows()
}

function markUnsettledCancelled(): void {
  for (const row of rows.value) {
    if (row.phase !== 'settled') row.phase = 'cancelled'
  }
  live.settled = rows.value.length
}

function onSignal(event: RunEvent): void {
  switch (event.type) {
    case 'run.started': {
      if (runId.value !== null && runId.value !== event.runId) return
      if (runId.value === null && phase.value !== 'preparing') return
      runId.value = event.runId
      outcomeAnnounced = false
      applyStarted(event.files)
      summary.value = null
      phase.value = 'running'
      return
    }
    case 'file.started': {
      if (!matches(event.runId)) return
      updateRow(event.id, { phase: 'running' })
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
      if (!matches(event.runId)) return
      if (outcomeAnnounced) { runId.value = null; phase.value = 'cancelled'; return }
      clearCancelTimer()
      summary.value = event.summary
      phase.value = event.summary.cancelled ? 'cancelled' : 'done'
      if (event.summary.cancelled) markUnsettledCancelled()
      runId.value = null
      if (event.summary.cancelled) {
        // 图床成片失败时这一轮是我们自己掐掉的，得把原因摆出来，
        // 否则用户只看到「已取消」，以为是自己点错了。
        outcomeAnnounced = true
        if (event.summary.abortReason) danger(event.summary.abortReason)
        else warning(t('toast.runCancelled'))
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
      clearCancelTimer()
      if (event.summary) summary.value = event.summary
      phase.value = 'cancelled'
      markUnsettledCancelled()
      runId.value = null
      if (outcomeAnnounced) return
      outcomeAnnounced = true
      if (event.summary?.abortReason) danger(event.summary.abortReason)
      else warning(t('toast.runCancelled'))
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
 */
function adoptSnapshot(snapshot: CurrentRun): void {
  if (!snapshot.runId) return
  // 这一轮的结果已经在界面上了（自己刚跑完），不要用快照再盖一遍。
  if (summary.value !== null && summary.value.runId === snapshot.runId) return
  // 已经结束、而且这个页面早就见过它（比如用户跑完又清空了队列）：
  // 重连时别把旧结果重新变出来。
  if (!snapshot.running && lastAdoptedRunId === snapshot.runId) return
  lastAdoptedRunId = snapshot.runId

  runId.value = snapshot.runId
  summary.value = snapshot.summary ?? null
  phase.value = snapshot.running
    ? 'running'
    : snapshot.summary?.cancelled
      ? 'cancelled'
      : 'done'

  applyStarted(snapshot.files ?? [])

  for (const result of snapshot.results ?? []) {
    const row = rowForResult(result.id, result.path) ?? rowFromResult(result)
    if (!rowAt(result.id) && !rowIndex.has(row.id)) pushRow(row)
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
  if (busy.value) return
  let snapshot: CurrentRun
  try {
    snapshot = await getCurrentRun()
  } catch {
    return
  }
  // 请求飞在路上时用户可能自己点了开始，这时别用快照把新的一轮盖掉。
  if (busy.value) return
  adoptSnapshot(snapshot)
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
    row.total = row.images ?? 0
    row.result = null
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
  if (busy.value) {
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
function applyPreview(result: PreviewResult, touched: readonly string[]): void {
  const byPath = new Map(result.files.map(file => [pathKey(file.path), file]))
  const touchedKeys = new Set(touched.map(pathKey))

  for (const row of rows.value) {
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

  seedImagesFromRows()
}

async function start(paths: string[], settings: Settings): Promise<boolean> {
  if (busy.value) return false
  if (paths.length === 0) {
    warning(t('toast.queueEmpty'))
    return false
  }

  clearCancelTimer()
  phase.value = 'preparing'
  runId.value = null
  summary.value = null
  resetRowsForRun()

  if (!connected.value) {
    warning(t('toast.eventsNotReady'))
  }

  try {
    const started = await requestStartRun(paths, settings)
    if (typeof started.runId !== 'string' || started.runId === '') {
      throw new Error(t('error.noRunId'))
    }
    if (runId.value === null && phase.value === 'preparing') {
      runId.value = started.runId
    }
    if (phase.value === 'preparing') phase.value = 'running'
    return true
  } catch (err) {
    if (runId.value !== null) return true
    phase.value = 'idle'
    danger(describeError(err, t('error.startRun')))
    return false
  }
}

async function cancel(): Promise<void> {
  if (phase.value !== 'running') return
  if (runId.value === null) {
    warning(t('toast.noRunId'))
    return
  }

  phase.value = 'cancelling'
  const cancellingRunId = runId.value
  clearCancelTimer()
  cancelTimer = window.setTimeout(() => {
    cancelTimer = null
    if (phase.value === 'cancelling') {
      phase.value = 'cancelled'
      markUnsettledCancelled()
      warning(t('toast.cancelUnconfirmed'))
    }
  }, 5000)

  try {
    await requestCancelRun(cancellingRunId)
  } catch (err) {
    clearCancelTimer()
    phase.value = 'running'
    danger(describeError(err, t('error.cancelRun')))
  }
}

function removeRow(id: string): void {
  if (busy.value) return
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
  if (busy.value) {
    warning(t('toast.clearLocked'))
    return
  }
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
    connected,
    reconnecting,
    busy,
    totalCount,
    completedCount,
    revealTarget,
    connect,
    disconnect,
    addPaths,
    applyPreview,
    start,
    cancel,
    removeRow,
    clearQueue,
  }
}
