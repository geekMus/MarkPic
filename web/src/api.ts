import type {
  DialogResult,
  Health,
  OkResult,
  PreviewResult,
  RunStartResult,
  Settings,
} from './types'

/**
 * 本地服务地址：仅用于错误文案，请求本身走同源 /api。
 * exe 在 17860 被占用时会自动换端口，所以不能硬编码端口，取当前页面 origin。
 */
export function serviceOrigin(): string {
  return window.location.origin
}

/** 统一的 API 错误：保留 HTTP 状态码与服务端 error.code。 */
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

type RequestInitLite = {
  method?: 'GET' | 'POST' | 'PUT'
  body?: unknown
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function isApiErrorBody(value: unknown): value is { error: { code: string; message: string } } {
  if (!isRecord(value) || !isRecord(value.error)) return false
  return typeof value.error.code === 'string' && typeof value.error.message === 'string'
}

async function request<T>(path: string, init: RequestInitLite = {}): Promise<T> {
  const hasBody = init.body !== undefined
  let response: Response
  try {
    response = await fetch(path, {
      method: init.method ?? 'GET',
      headers: hasBody
        ? { Accept: 'application/json', 'Content-Type': 'application/json; charset=utf-8' }
        : { Accept: 'application/json' },
      body: hasBody ? JSON.stringify(init.body) : undefined,
    })
  } catch {
    throw new ApiError(0, 'network', `无法连接本地服务（${serviceOrigin()}）`)
  }

  const raw = await response.text()
  let payload: unknown = null
  if (raw.length > 0) {
    try {
      payload = JSON.parse(raw) as unknown
    } catch {
      payload = raw
    }
  }

  if (!response.ok) {
    if (isApiErrorBody(payload)) {
      throw new ApiError(response.status, payload.error.code, payload.error.message)
    }
    const text = typeof payload === 'string' ? payload.trim().slice(0, 200) : ''
    throw new ApiError(
      response.status,
      `http-${response.status}`,
      text || `请求失败（HTTP ${response.status}）`,
    )
  }

  return payload as T
}

/** 把任意异常收敛成一句可展示的文案。 */
export function describeError(error: unknown, fallback: string): string {
  if (error instanceof ApiError) return error.message.trim() || fallback
  if (error instanceof Error) return error.message.trim() || fallback
  return fallback
}

/** 原生对话框不可用（契约规定 501）。 */
export function isDialogUnsupported(error: unknown): boolean {
  return error instanceof ApiError && error.status === 501
}

export function getHealth(): Promise<Health> {
  return request<Health>('/api/health')
}

export function getSettings(): Promise<Settings> {
  return request<Settings>('/api/settings')
}

export function putSettings(settings: Settings): Promise<Settings> {
  return request<Settings>('/api/settings', { method: 'PUT', body: settings })
}

export async function pickFiles(): Promise<string[]> {
  const data = await request<DialogResult>('/api/dialog/files', { method: 'POST' })
  return data.paths
}

export async function pickFolder(): Promise<string[]> {
  const data = await request<DialogResult>('/api/dialog/folder', { method: 'POST' })
  return data.paths
}

export function preview(paths: string[], settings?: Partial<Settings>): Promise<PreviewResult> {
  return request<PreviewResult>('/api/preview', {
    method: 'POST',
    body: { paths, settings },
  })
}

export function startRun(paths: string[], settings?: Partial<Settings>): Promise<RunStartResult> {
  return request<RunStartResult>('/api/run', {
    method: 'POST',
    body: { paths, settings },
  })
}

export function cancelRun(runId: string): Promise<OkResult> {
  return request<OkResult>('/api/run/cancel', { method: 'POST', body: { runId } })
}

export function revealPath(path: string): Promise<OkResult> {
  return request<OkResult>('/api/reveal', { method: 'POST', body: { path } })
}

export function openTarget(target: string): Promise<OkResult> {
  return request<OkResult>('/api/open', { method: 'POST', body: { target } })
}
