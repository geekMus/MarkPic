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
}

export type ImageStatus = 'uploaded' | 'reused' | 'remote' | 'missing' | 'failed' | 'pending'

export type ImageItem = {
  target: string
  absolutePath: string
  kind: 'markdown' | 'html'
  line: number
  status: ImageStatus
  url?: string
  error?: string
}

export type FileStatus = 'ok' | 'partial' | 'no-images' | 'failed' | 'preview'

export type FileResult = {
  id: string
  path: string
  name: string
  dir: string
  status: FileStatus
  images: number
  uploaded: number
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
  failedImages: number
  cancelled: boolean
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
  | { type: 'run.cancelled'; runId: string }
  | { type: 'log'; level: 'info' | 'warn' | 'error'; message: string }

export type Health = {
  version: string
  endpoint: string
  picgo: { online: boolean; detail: string }
}

export type DialogResult = { paths: string[] }

export type PreviewResult = { files: FileResult[]; summary: RunSummary }

export type RunStartResult = { runId: string }

export type OkResult = { ok: boolean }
