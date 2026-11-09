/**
 * 契约类型：逐字对照 docs/API.md（已冻结），不要在前端另起字段名。
 */

export type Settings = {
  endpoint: string
  concurrency: number
  retries: number
  output: 'sidecar' | 'in-place' | 'directory'
  suffix: string
  outDir: string
  backup: boolean
  dryRun: boolean
  /** 复用上一次运行留下的上传结果，同一张图不再重传。 */
  reuseUploads: boolean
  /**
   * 远程图片怎么处理：ignore 原样留着；check 只检测能不能打开（不改文档）；
   * download 下载回本地并把链接改成相对路径。
   */
  remote: RemoteMode
  /** download 模式下图片落到文档旁边的哪个子目录。 */
  remoteDir: string
}

export type RemoteMode = 'ignore' | 'check' | 'download'

export type ImageStatus =
  | 'uploaded'
  | 'reused'
  | 'cached'
  | 'remote'
  | 'missing'
  | 'failed'
  | 'cancelled'
  | 'pending'
  /** download 模式：远程图已经下载到本地。 */
  | 'localized'
  /** download 模式：本地图片原样保留（这个模式不上传）。 */
  | 'local-kept'
  /** check 模式：地址能打开，拿到的确实是图片。 */
  | 'remote-ok'
  /** check 模式：地址打不开，或者返回的不是图片。 */
  | 'remote-bad'

export type ImageItem = {
  target: string
  absolutePath: string
  kind: 'markdown' | 'html'
  line: number
  status: ImageStatus
  url?: string
  error?: string
}

export type FileStatus =
  | 'ok'
  | 'partial'
  | 'no-images'
  | 'failed'
  | 'cancelled'
  | 'preview'
  /** check 模式：只检测、不写文件。 */
  | 'checked'

export type FileResult = {
  id: string
  path: string
  name: string
  dir: string
  status: FileStatus
  images: number
  uploaded: number
  /** 这一处替换是靠复用（本轮去重或上次的缓存）拿到的地址。 */
  reused?: number
  failed: number
  skipped: number
  /** download 模式：这一篇里有几张远程图被下载到了本地。 */
  localized?: number
  /**
   * download 模式：去重之后真正下载了几个文件。
   *
   * 同一张图在一篇文档里被引用 20 次时 localized 是 20，可 images/ 里只有一个
   * 文件 —— 只显示 20 会让人以为另外 19 张失败了。
   */
  localizedUnique?: number
  /** check 模式：这一篇里有几个远程地址打不开（或不是图片）。 */
  dead?: number
  /** check 模式：这一篇里一共检测了几个远程地址。 */
  checked?: number
  /** 碰不了的远程地址（ftp://、data: 之类）：既不算检测过，也不算失效。 */
  remoteUnsupported?: number
  /** download 模式：远程地址这次没拿到图（连不上、超时）。同时也算在 failed 里。 */
  remoteFailed?: number
  outputPath?: string
  error?: string
  warnings?: string[]
  items: ImageItem[]
}

export type RunSummary = {
  runId: string
  files: number
  ok: number
  partial: number
  failed: number
  noImages: number
  images: number
  uploaded: number
  /** 这一轮靠缓存省下的去重图片数（不是引用数）。 */
  reused: number
  failedImages: number
  cancelled: boolean
  /**
   * 这一轮被提前掐掉的原因（目前只有「图床成片失败」一种）。
   * 取消事件也带汇总，所以界面能说清楚「为什么停了」。
   */
  abortReason?: string
  /** download 模式：这一轮把几张远程图下载到了本地。 */
  localized?: number
  /** download 模式：去重之后真正下载了几个文件（localized 是按引用处算的）。 */
  localizedUnique?: number
  /** check 模式：这一轮检测了几个远程地址、其中几个打不开。 */
  remoteChecked?: number
  remoteDead?: number
  /** 碰不了的远程地址（ftp://、data: 之类）：既不算检测过，也不算失效。 */
  remoteUnsupported?: number
  /** download 模式：远程地址这次没拿到图（连不上、超时）。同时也算在 failed 里。 */
  remoteFailed?: number
  /** check 模式：这一轮有几个文件只是被检测（没写输出）。 */
  filesChecked?: number
  /** 上次已经下过、这次没再下载的远程图张数（按去重后的图片算）。 */
  remoteReused?: number
  durationMs: number
}

/** run.started 中携带的文件信息。 */
export type RunFileInfo = {
  id: string
  path: string
  name: string
  dir: string
  images: number
}

export type RunEvent =
  | { type: 'run.started'; runId: string; files: RunFileInfo[] }
  | { type: 'file.started'; runId: string; id: string }
  | { type: 'file.progress'; runId: string; id: string; uploaded: number; total: number }
  | { type: 'file.finished'; runId: string; id: string; result: FileResult }
  | { type: 'run.finished'; runId: string; summary: RunSummary }
  | { type: 'run.cancelled'; runId: string; summary?: RunSummary }
  | { type: 'log'; level: 'info' | 'warn' | 'error'; message: string }

export type Health = {
  version: string
  endpoint: string
  picgo: {
    online: boolean
    detail: string
    /** 这次运行到底要不要 PicGo：只检测 / 下载回本地时不需要，掉线也不算错。 */
    needed: boolean
  }
  /** 设置与 WebView 缓存所在的位置（默认是 exe 旁边的 data 目录）。 */
  dataDir: string
  /** 没能放到 exe 旁边时的原因，正常情况下是空。 */
  dataNote?: string
  /** 设置文件读不出来时的说明（此时跑的是默认值），正常情况是空。 */
  settingsNote?: string
}

export type DialogResult = { paths: string[] }

export type PreviewResult = { files: FileResult[]; summary: RunSummary }

export type RunStartResult = { runId: string }

/**
 * 服务端眼里的「这一轮」。刷新页面之后前端内存里的 runId 就没了，
 * 只能问服务端现在在跑什么、已经出了哪些结果。
 */
export type CurrentRun = {
  running: boolean
  done: boolean
  runId?: string
  files?: RunFileInfo[]
  results?: FileResult[]
  summary?: RunSummary
}

export type OkResult = { ok: boolean }

/** 更新包（清单里给出的地址、校验值与大小）。 */
export type UpdateAsset = {
  url: string
  sha256: string
  size: number
}

export type UpdateState =
  | 'idle'
  | 'checking'
  | 'latest'
  | 'available'
  | 'downloading'
  | 'installing'
  | 'restarting'
  | 'error'

/** /api/update/status 与 /api/update/check 共用的形状。 */
export type UpdateStatus = {
  state: UpdateState
  current: string
  latest: string
  tag: string
  notes: string
  published: string
  asset: UpdateAsset
  received: number
  total: number
  error: string
  releasePage: string
}
