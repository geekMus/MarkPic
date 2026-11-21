import type {
  DialogResult,
  Health,
  OkResult,
  RunOp,
  RunStartResult,
  ScanStartResult,
  Settings,
  UpdateStatus,
} from './types'
import { t } from './i18n'

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
  readonly code: string

  constructor(code: string, message: string) {
    super(message)
    this.name = 'ApiError'
    this.code = code
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

/** 把 bridge 返回的失败信封翻成 ApiError。 */
function toError(reply: Reply): ApiError {
  const code = reply.code ?? 'unknown'
  const message = (reply.message ?? '').trim()
  return new ApiError(code, message || t('error.bridge'))
}

/** 调一个 Go 侧方法，成功时返回 data。 */
async function call<T>(method: string, params: unknown = {}): Promise<T> {
  const bridge = window.markpic
  if (!bridge) {
    // 在浏览器里直接开 Vite 页面调试时没有 bridge，给一句能看懂的提示。
    throw new ApiError('no-bridge', t('error.noBridge'))
  }
  let reply: Reply
  try {
    reply = await bridge(method, JSON.stringify(isRecord(params) ? params : {}))
  } catch (error) {
    throw new ApiError('bridge-failed', error instanceof Error ? error.message : String(error))
  }
  if (!reply || !reply.ok) {
    throw toError(reply ?? { ok: false, code: 'empty', message: '' })
  }
  return reply.data as T
}

/**
 * 首屏已经画好：让原生窗口露面（见 cmd/markpic/startup_windows.go）。
 *
 * 窗口命令走 go-webview2 的直接绑定（和 cmpWinMinimize 一样），没有参数；
 * 浏览器预览里没有这个函数，所以是可选调用。
 */
export function markWindowReady(): void {
  const bridge = window as Window & { cmpWinReady?: () => Promise<unknown> }
  void bridge.cmpWinReady?.()
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

/**
 * 告诉后端界面现在用哪种语言。
 *
 * 后端回的文字（逐图失败原因、PicGo 提示、更新错误）由它自己出，所以语言得同步过去；
 * 这是一条独立通道，不再随每次调用捎带 —— 以前 api.ts 给每个请求都塞一个 lang，
 * 而后端从来没读过它，切到英文之后后台文案仍然是中文。
 */
export function setLang(lang: string): Promise<{ lang: string }> {
  return call<{ lang: string }>('i18n.set', { lang })
}

export function getHealth(): Promise<Health> {
  return call<Health>('health')
}

export function getSettings(): Promise<Settings> {
  return call<Settings>('settings.get')
}

export function putSettings(settings: Settings): Promise<Settings> {
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

/**
 * 让后端开扫一批路径。
 *
 * 回来的只是「已经收下了」：每个路径扫完会推一条 scan.file 事件回来。
 * scanId 由前端生成，取消和事件归属都靠它认。
 */
export function scanStart(params: {
  scanId: string
  paths: string[]
  /** true = 只发现 Markdown（文件夹卡片）；false = 逐份统计图片数（文件卡片）。 */
  discover?: boolean
}): Promise<ScanStartResult> {
  return call<ScanStartResult>('scan.start', params)
}

/** 取消一批扫描；带 path 就只取消那一份。 */
export function cancelScan(params: { scanId: string; path?: string }): Promise<{ cancelled: boolean }> {
  return call<{ cancelled: boolean }>('scan.cancel', params)
}

/**
 * 开始一轮转换。
 *
 * op 是用户点了什么（上传 / 检测 / 下载），不是设置：设置里那个 remote 说的是
 * 「文档里已经写着的网络图片怎么办」，跟这一轮做什么无关。
 */
export function startRun(op: RunOp, paths: string[]): Promise<RunStartResult> {
  return call<RunStartResult>('run.start', { op, paths })
}

export function cancelRun(runId: string): Promise<OkResult> {
  return call<OkResult>('run.cancel', { runId })
}

export function revealPath(path: string): Promise<OkResult> {
  return call<OkResult>('reveal', { path })
}

export function openTarget(target: string): Promise<OkResult> {
  return call<OkResult>('open', { target })
}

/** 更新检查 / 安装 / 状态。 */
export function getUpdateStatus(): Promise<UpdateStatus> {
  return call<UpdateStatus>('update.status')
}

export function checkUpdate(): Promise<UpdateStatus> {
  return call<UpdateStatus>('update.check')
}

export function installUpdate(): Promise<UpdateStatus> {
  return call<UpdateStatus>('update.install')
}
