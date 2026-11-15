import type {
  CurrentRun,
  DialogResult,
  Health,
  OkResult,
  PreviewResult,
  RemoteMode,
  RunStartResult,
  ScanStartResult,
  Settings,
  UpdateStatus,
} from './types'
import { langHeader, t } from './i18n'

/**
 * 界面与 Go 之间的通道。
 *
 * 这是个纯桌面程序：没有本地 HTTP 服务，所有调用都走窗口自带的 bridge
 * （Go 侧 `w.Bind("markpic", …)` 把调用转成 `app.Dispatch`）。参数按约定编成
 * 一个 JSON 字符串再传 —— WebView2 会对每个参数单独 json.Unmarshal，直接传对象
 * 到不了 Go 那一侧的 json.RawMessage。
 */

/** Go 侧统一回的信封。 */
type Reply = {
  ok: boolean
  data?: unknown
  code?: string
  message?: string
}

declare global {
  interface Window {
    /** go-webview2 的 Bind 是 Promise 形式的 RPC：调用返回的是结果，不是「发出去就算」。 */
    markpic?: (method: string, params: string) => Promise<Reply>
    /** Go 侧推事件用的入口，由 useRun 挂上（见 cmd/markpic/window_windows.go）。 */
    __markpicEvents?: (payload: unknown) => void
  }
}

/** 统一的 API 错误：保留服务端的错误码与服务端给的文案。 */
export class ApiError extends Error {
  readonly status: number
  readonly code: string

  constructor(status: number, code: string, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

/** 旧文案里会提到「本地服务」；现在没有服务了，保留这个函数只为兼容调用点。 */
export function serviceOrigin(): string {
  return 'MarkPic'
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

/** 把 bridge 返回的失败信封翻成 ApiError。 */
function toError(reply: Reply): ApiError {
  const code = reply.code ?? 'unknown'
  const message = (reply.message ?? '').trim()
  return new ApiError(0, code, message || t('error.bridge'))
}

/** 调一个 Go 侧方法，成功时返回 data。 */
async function call<T>(method: string, params: unknown = {}): Promise<T> {
  const bridge = window.markpic
  if (!bridge) {
    // 在浏览器里直接开 Vite 页面调试时没有 bridge，给一句能看懂的提示。
    throw new ApiError(0, 'no-bridge', t('error.noBridge'))
  }
  let reply: Reply
  try {
    reply = await bridge(method, JSON.stringify({ ...(isRecord(params) ? params : {}), lang: langHeader() }))
  } catch (error) {
    throw new ApiError(0, 'bridge-failed', error instanceof Error ? error.message : String(error))
  }
  if (!reply || !reply.ok) {
    throw toError(reply ?? { ok: false, code: 'empty', message: '' })
  }
  return reply.data as T
}

/** 把任意异常收敛成一句可展示的文案。 */
export function describeError(error: unknown, fallback: string): string {
  if (error instanceof ApiError) return error.message.trim() || fallback
  if (error instanceof Error) return error.message.trim() || fallback
  return fallback
}

/** 原生对话框不可用（当前平台上没有系统文件对话框）。 */
export function isDialogUnsupported(error: unknown): boolean {
  return error instanceof ApiError && error.code === 'unsupported'
}

/** 应用与数据目录的基本信息（语言由参数带上，Go 侧据此回本地化文案）。 */
export type Hello = {
  version: string
  dataDir: string
  dataNote: string
  lang: string
}

export function hello(lang: string): Promise<Hello> {
  return call<Hello>('app.hello', { lang })
}

export function setLang(lang: string): Promise<{ lang: string }> {
  return call<{ lang: string }>('i18n.set', { lang })
}

export function getHealth(): Promise<Health> {
  return call<Health>('health')
}

export function getSettings(): Promise<Settings> {
  return call<Settings>('settings.get')
}

export function putSettings(settings: Partial<Settings>): Promise<Settings> {
  return call<Settings>('settings.put', settings)
}

/** 只改关闭行为：关窗时勾了「不再询问」时用，不碰设置页里没保存的草稿。 */
export function setCloseAction(
  action: Settings['closeAction'],
): Promise<{ closeAction: Settings['closeAction'] }> {
  return call<{ closeAction: Settings['closeAction'] }>('settings.closeAction', { action })
}

export async function pickFiles(): Promise<string[]> {
  const data = await call<DialogResult>('dialog.files')
  return data.paths
}

export async function pickFolder(): Promise<string[]> {
  const data = await call<DialogResult>('dialog.folder')
  return data.paths
}

export function preview(paths: string[], settings?: Partial<Settings>): Promise<PreviewResult> {
  return call<PreviewResult>('preview', { paths, settings })
}

/**
 * 让后端开扫一批文件。
 *
 * 回来的只是「已经收下了」：每个文件扫完会推一条 scan.file 事件回来。
 * scanId 由前端生成，取消和事件归属都靠它认。
 */
export function scanStart(params: {
  scanId: string
  paths: string[]
  settings?: Partial<Settings>
}): Promise<ScanStartResult> {
  return call<ScanStartResult>('scan.start', params)
}

/** 取消一批扫描；带 path 就只取消那一份。 */
export function cancelScan(params: { scanId: string; path?: string }): Promise<{ cancelled: boolean }> {
  return call<{ cancelled: boolean }>('scan.cancel', params)
}

export function startRun(paths: string[], settings?: Partial<Settings>): Promise<RunStartResult> {
  return call<RunStartResult>('run.start', { paths, settings })
}

export function cancelRun(runId: string): Promise<OkResult> {
  return call<OkResult>('run.cancel', { runId })
}

/** Go 侧那一页现在在跑哪一轮、已经出了哪些结果（界面重载后靠它接回来）。 */
export function getCurrentRun(mode?: RemoteMode): Promise<CurrentRun> {
  return call<CurrentRun>('run.current', mode === undefined ? {} : { mode })
}

export function revealPath(path: string): Promise<OkResult> {
  return call<OkResult>('reveal', { path })
}

export function openTarget(target: string): Promise<OkResult> {
  return call<OkResult>('open', { target })
}

/** 当前更新状态（不联网，读的是 Go 侧记住的那份）。 */
export function getUpdateStatus(): Promise<UpdateStatus> {
  return call<UpdateStatus>('update.status')
}

/**
 * 检查更新。检查失败不会抛异常：Go 侧把原因写进 status.error，
 * 这样对话框可以把它当成一种状态展示，而不是一个红色的请求失败。
 */
export function checkUpdate(): Promise<UpdateStatus> {
  return call<UpdateStatus>('update.check')
}

/** 下载、校验、替换并重启。之后用 getUpdateStatus 轮询进度。 */
export function installUpdate(): Promise<UpdateStatus> {
  return call<UpdateStatus>('update.install')
}