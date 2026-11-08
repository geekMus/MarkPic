import { computed, reactive, ref } from 'vue'
import {
  cancelRun as requestCancelRun,
  describeError,
  startRun as requestStartRun,
} from '../api'
import type {
  FileResult,
  PreviewResult,
  RunEvent,
  RunFileInfo,
  RunSummary,
  Settings,
} from '../types'
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
  uploaded: number
  total: number
  /** 预览跑过之后仍然没有任何文件与之匹配（路径不存在，或目录里没有 Markdown）。 */
  missing?: boolean
}

export interface RunRowView extends RunRow {
  result: FileResult | null
}

export interface RunStats {
  files: number
  images: number
  uploaded: number
  failed: number
  ok: number
  partial: number
  failedFiles: number
  noImages: number
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
const results = reactive(new Map<string, FileResult>())
const summary = ref<RunSummary | null>(null)
const runId = ref<string | null>(null)
const connected = ref(false)
const reconnecting = ref(false)

let source: EventSource | null = null
let reconnectTimer: number | null = null
let reconnectDelay = 800
let intentionalClose = false
let cancelTimer: number | null = null
let localSeq = 0

const busy = computed(
  () => phase.value === 'preparing' || phase.value === 'running' || phase.value === 'cancelling',
)

const rowViews = computed<RunRowView[]>(() =>
  rows.value.map(row => ({ ...row, result: results.get(row.id) ?? null })),
)

const totalCount = computed(() => rows.value.length)

const completedCount = computed(
  () => rowViews.value.filter(row => row.phase === 'settled' || row.phase === 'cancelled').length,
)

const stats = computed<RunStats>(() => {
  const finished = summary.value
  if (finished) {
    return {
      files: finished.files,
      images: finished.images,
      uploaded: finished.uploaded,
      failed: finished.failedImages,
      ok: finished.ok,
      partial: finished.partial,
      failedFiles: finished.failed,
      noImages: finished.noImages,
    }
  }

  let images = 0
  let uploaded = 0
  let failed = 0
  let ok = 0
  let partial = 0
  let failedFiles = 0
  let noImages = 0

  for (const row of rowViews.value) {
    const result = row.result
    if (result) {
      images += result.images
      uploaded += result.uploaded
      failed += result.failed
      if (result.status === 'ok') ok += 1
      else if (result.status === 'partial') partial += 1
      else if (result.status === 'failed') failedFiles += 1
      else if (result.status === 'no-images') noImages += 1
    } else {
      images += row.images ?? row.total
      uploaded += row.uploaded
    }
  }

  return { files: rows.value.length, images, uploaded, failed, ok, partial, failedFiles, noImages }
})

/** 转换结束后「打开输出目录」的目标：最后一个成功文件的产物，否则首行所在目录。 */
const revealTarget = computed<string | null>(() => {
  for (let index = rows.value.length - 1; index >= 0; index -= 1) {
    const result = results.get(rows.value[index].id)
    if (result && (result.status === 'ok' || result.status === 'partial') && result.outputPath) {
      return result.outputPath
    }
  }
  const first = rows.value[0]
  return first ? first.dir : null
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

function updateRow(
  id: string,
  patch: Partial<Pick<RunRow, 'phase' | 'uploaded' | 'total' | 'images'>>,
): void {
  const index = rows.value.findIndex(row => row.id === id)
  if (index < 0) return
  rows.value[index] = { ...rows.value[index], ...patch }
}

function ensureRowFromResult(result: FileResult): void {
  if (rows.value.some(row => row.id === result.id)) return
  rows.value.push({
    id: result.id,
    path: result.path,
    name: result.name,
    dir: result.dir,
    images: result.images,
    phase: 'settled',
    uploaded: result.uploaded,
    total: result.images,
  })
}

function applyStarted(files: RunFileInfo[]): void {
  const started = files.map(file => ({
    id: file.id,
    path: file.path,
    name: file.name,
    dir: file.dir,
    images: file.images,
    phase: 'queued' as const,
    uploaded: 0,
    total: file.images,
  }))
  // 后端只会回传真正找到的文件。用户加进来、但一个文件都没匹配上的路径要留在队列里，
  // 否则一点「开始转换」它就凭空消失，用户无从知道这条路径被忽略了。
  const known = new Set(files.map(file => pathKey(file.path)))
  const kept = rows.value.filter(row => row.missing && !known.has(pathKey(row.path)))
  rows.value = [...started, ...kept]
  results.clear()
}

function markUnsettledCancelled(): void {
  rows.value = rows.value.map(row =>
    row.phase === 'settled' ? row : { ...row, phase: 'cancelled' },
  )
}

function onSignal(event: RunEvent): void {
  switch (event.type) {
    case 'run.started': {
      if (runId.value !== null && runId.value !== event.runId) return
      if (runId.value === null && phase.value !== 'preparing') return
      runId.value = event.runId
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
      updateRow(event.id, {
        phase: 'running',
        uploaded: event.uploaded,
        total: event.total,
        images: event.total,
      })
      return
    }
    case 'file.finished': {
      if (!matches(event.runId)) return
      results.set(event.id, event.result)
      ensureRowFromResult(event.result)
      updateRow(event.id, {
        phase: 'settled',
        uploaded: event.result.uploaded,
        total: event.result.images,
        images: event.result.images,
      })
      return
    }
    case 'run.finished': {
      if (!matches(event.runId)) return
      clearCancelTimer()
      summary.value = event.summary
      phase.value = event.summary.cancelled ? 'cancelled' : 'done'
      if (event.summary.cancelled) markUnsettledCancelled()
      runId.value = null
      if (event.summary.cancelled) {
        warning('转换已取消')
      } else if (event.summary.failedImages > 0) {
        warning(`转换完成，${event.summary.failedImages} 张图片失败`)
      } else {
        success(`转换完成 · 上传 ${event.summary.uploaded} 张图片`)
      }
      return
    }
    case 'run.cancelled': {
      if (!matches(event.runId)) return
      clearCancelTimer()
      phase.value = 'cancelled'
      markUnsettledCancelled()
      runId.value = null
      warning('转换已取消')
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

function handleRaw(raw: string): void {
  if (raw.length === 0) return
  let parsed: unknown
  try {
    parsed = JSON.parse(raw) as unknown
  } catch {
    console.warn('[events] 无法解析的事件负载')
    return
  }
  if (!isRunEvent(parsed)) {
    console.warn('[events] 忽略未知事件')
    return
  }
  onSignal(parsed)
}

function connect(): void {
  if (source !== null) return
  intentionalClose = false

  const es = new EventSource('/api/events')
  source = es

  es.onopen = () => {
    connected.value = true
    reconnectDelay = 800
    if (reconnecting.value) {
      reconnecting.value = false
      success('已恢复实时连接')
    }
  }

  // 后端每条 SSE 都带具名事件行（event: run.started …），具名事件不会触发
  // onmessage，必须按类型逐个注册监听；onmessage 仅作为无名事件的兜底。
  for (const type of KNOWN_EVENT_TYPES) {
    es.addEventListener(type, event => {
      handleRaw((event as MessageEvent).data)
    })
  }

  es.onmessage = event => {
    handleRaw(event.data)
  }

  es.onerror = () => {
    connected.value = false
    if (source === es) {
      es.close()
      source = null
    }
    if (!intentionalClose) scheduleReconnect()
  }
}

function scheduleReconnect(): void {
  if (reconnectTimer !== null) return
  if (!reconnecting.value) {
    reconnecting.value = true
    warning('实时连接已断开，正在重连')
  }
  const delay = reconnectDelay
  reconnectDelay = Math.min(8000, reconnectDelay * 2)
  reconnectTimer = window.setTimeout(() => {
    reconnectTimer = null
    connect()
  }, delay)
}

function disconnect(): void {
  intentionalClose = true
  if (reconnectTimer !== null) {
    window.clearTimeout(reconnectTimer)
    reconnectTimer = null
  }
  if (source !== null) {
    source.close()
    source = null
  }
  connected.value = false
  reconnecting.value = false
}

function resetOutcome(): void {
  phase.value = 'idle'
  summary.value = null
  runId.value = null
  results.clear()
  rows.value = rows.value.map(row => ({ ...row, phase: 'queued', uploaded: 0, total: row.images ?? 0 }))
}

/** 加入队列；返回真正新增的路径，供调用方做预览。 */
function addPaths(input: string[]): string[] {
  if (busy.value) {
    warning('转换进行中，暂不能修改队列')
    return []
  }

  if (phase.value === 'done' || phase.value === 'cancelled') resetOutcome()

  const known = new Set(rows.value.map(row => pathKey(row.path)))
  const added: string[] = []

  for (const raw of input) {
    const path = raw.trim().replace(/^"(.*)"$/, '$1').trim()
    if (path === '') continue
    const key = pathKey(path)
    if (key === '' || known.has(key)) continue
    known.add(key)
    added.push(path)
    const { name, dir } = splitPath(path)
    rows.value.push({
      id: `local-${++localSeq}`,
      path,
      name,
      dir,
      images: null,
      phase: 'queued',
      uploaded: 0,
      total: 0,
    })
  }

  return added
}

/** 用 /api/preview 的结果补齐队列行的图片数量（目录行按前缀汇总）。 */
function applyPreview(result: PreviewResult): void {
  const byPath = new Map(result.files.map(file => [pathKey(file.path), file]))

  rows.value = rows.value.map(row => {
    const exact = byPath.get(pathKey(row.path))
    if (exact) {
      // 预检结果也存进 results：行的 images 有了，逐图明细（L 行）才有出处，
      // 否则展开一行 8 张图的文件会得到「没有可处理的本地图片」这种自相矛盾的话。
      if (!results.has(row.id)) results.set(row.id, exact)
      return { ...row, images: exact.images, total: exact.images, missing: false }
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
    return matched ? { ...row, images: sum, total: sum, missing: false } : { ...row, missing: true }
  })
}

async function start(paths: string[], settings: Settings): Promise<boolean> {
  if (busy.value) return false
  if (paths.length === 0) {
    warning('队列为空，请先添加文件')
    return false
  }

  clearCancelTimer()
  phase.value = 'preparing'
  runId.value = null
  summary.value = null
  results.clear()
  rows.value = rows.value.map(row => ({ ...row, phase: 'queued', uploaded: 0, total: row.images ?? 0 }))

  if (!connected.value) {
    warning('实时进度连接未就绪，转换进度可能不会更新')
  }

  try {
    const started = await requestStartRun(paths, settings)
    if (typeof started.runId !== 'string' || started.runId === '') {
      throw new Error('本地服务未返回 runId')
    }
    if (runId.value === null && phase.value === 'preparing') {
      runId.value = started.runId
    }
    if (phase.value === 'preparing') phase.value = 'running'
    return true
  } catch (err) {
    if (runId.value !== null) return true
    phase.value = 'idle'
    danger(describeError(err, '启动转换失败'))
    return false
  }
}

async function cancel(): Promise<void> {
  if (phase.value !== 'running') return
  if (runId.value === null) {
    warning('未获取到运行标识，暂时无法取消')
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
      warning('已请求取消，但未收到完成事件')
    }
  }, 5000)

  try {
    await requestCancelRun(cancellingRunId)
  } catch (err) {
    clearCancelTimer()
    phase.value = 'running'
    danger(describeError(err, '取消失败'))
  }
}

function removeRow(id: string): void {
  if (busy.value) return
  rows.value = rows.value.filter(row => row.id !== id)
  results.delete(id)
  if (rows.value.length === 0) {
    phase.value = 'idle'
    summary.value = null
    runId.value = null
  }
}

function clearQueue(): void {
  if (busy.value) {
    warning('转换进行中，暂不能清空队列')
    return
  }
  clearCancelTimer()
  rows.value = []
  results.clear()
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
