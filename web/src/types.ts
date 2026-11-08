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
}

export type ImageStatus =
  | 'uploaded'
  | 'reused'
  | 'cached'
  | 'remote'
  | 'missing'
  | 'failed'
  | 'cancelled'
  | 'pending'

export type ImageItem = {
  target: string
  absolutePath: string
  kind: 'markdown' | 'html'
  line: number
  status: ImageStatus
  url?: string
  error?: string
}

export type FileStatus = 'ok' | 'partial' | 'no-images' | 'failed' | 'cancelled' | 'preview'

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
  picgo: { online: boolean; detail: string }
  /** 设置与 WebView 缓存所在的位置（默认是 exe 旁边的 data 目录）。 */
  dataDir: string
  /** 没能放到 exe 旁边时的原因，正常情况下是空。 */
  dataNote?: string
}

export type DialogResult = { paths: string[] }

export type PreviewResult = { files: FileResult[]; summary: RunSummary }

export type RunStartResult = { runId: string }

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
