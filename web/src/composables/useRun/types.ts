/**
 * 一轮作业的状态、队列行的形状，以及它带回来的统计。
 *
 * 这里只有类型、常量与空计数器：不读也不写任何运行期状态，谁都可以引用它，不会绕回来。
 */
import type { FileResult, ImageItem, RemoteMode } from '../../types'

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
   * 这一行的转换结果（或预检结果）。
   *
   * 结果直接挂在行上，行对象自己就是响应式的：某一行完成时只有那一行重渲染，
   * 不必重建整张列表。预检结果也用同一个字段，展开一行才有逐图明细可看。
   */
  result: FileResult | null
  /** 服务端为这次运行分配的 id（事件里的 id）。队列行的 id 由本地生成，两者都要能查到行。 */
  serverId?: string
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
}

/** 列表拿到的行就带着结果。保留这个别名，免得每个组件都改一遍类型。 */
export type RunRowView = RunRow

export interface RunStats {
  files: number
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

export const KNOWN_EVENT_TYPES = new Set<string>([
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
export interface LiveCounters {
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

export function emptyCounters(): LiveCounters {
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

export const PAGES: readonly RemoteMode[] = ['ignore', 'check', 'download']
