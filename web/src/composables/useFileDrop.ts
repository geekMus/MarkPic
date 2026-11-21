import { onBeforeUnmount, onMounted, ref } from 'vue'

/**
 * 把文件/目录拖进窗口就等于把它们加进队列。
 *
 * 路径从原生侧来：WebView2 的 DataTransfer 里只有 File 对象，没有本地路径
 * （`File.path` 早被 Chromium 删掉），从资源管理器拖文件时 `text/uri-list` 也是空的。
 * 所以真正的拖放在原生侧接管了 OLE 放置目标（见 cmd/markpic/drop_windows.go），
 * 拿到路径后调 `window.__markpicDrop(paths)`；拖入/离开则由
 * `window.__markpicDragOverlay(bool)` 驱动放置态。
 *
 * 下面那套 DOM 处理仍然保留，理由有两个：
 *  1. dragover/drop 一定要 preventDefault —— 原生接管失败时，不拦的话 WebView2
 *     会按浏览器默认行为直接导航到那个文件（file:///…），界面当场没了。
 *  2. 非 Windows 平台或原生接管失败时，这里还能从 text/uri-list、text/plain
 *     里捞一捞（拖一段文字路径也能用）。
 */
export interface FileDropHandlers {
  onPaths: (paths: string[]) => void
  /** 拖进来了，但拿不到本地路径；hadFiles 表示这次拖的确实是文件（可退回原生选择框） */
  onUnsupported: (hadFiles: boolean) => void
}

export function useFileDrop(handlers: FileDropHandlers) {
  const active = ref(false)
  // dragenter/dragleave 在子元素之间穿梭会成对乱跳，用计数抵消
  let depth = 0

  function fileURLToPath(value: string): string {
    const trimmed = value.trim()
    if (!trimmed.toLowerCase().startsWith('file:')) return ''
    let rest = trimmed.slice('file:///'.length - 1).replace(/^\/+/, '')
    try {
      rest = decodeURIComponent(rest)
    } catch {
      // 地址里有单独的 % 就原样用，反正后面还要交给服务端校验
    }
    return rest.replace(/\//g, '\\').trim()
  }

  function pathsFrom(data: DataTransfer | null): string[] {
    if (data === null) return []
    const out: string[] = []
    const seen = new Set<string>()

    const push = (raw: string): void => {
      const value = raw.trim()
      if (value === '' || seen.has(value.toLowerCase())) return
      seen.add(value.toLowerCase())
      out.push(value)
    }

    const uriList = data.getData('text/uri-list')
    if (uriList !== '') {
      for (const line of uriList.split(/\r?\n/)) {
        if (line.startsWith('#') || line.trim() === '') continue
        const path = fileURLToPath(line)
        if (path !== '') push(path)
      }
    }

    // 从资源管理器拖进来的文件，多数浏览器不给路径；能拿到就用
    for (const file of Array.from(data.files ?? [])) {
      const path = (file as File & { path?: string }).path
      if (typeof path === 'string' && path !== '') push(path)
    }

    if (out.length === 0) {
      const text = data.getData('text/plain')
      if (text !== '') {
        for (const line of text.split(/\r?\n/)) push(line)
      }
    }
    return out
  }

  function hasFiles(data: DataTransfer | null): boolean {
    if (data === null) return false
    return Array.from(data.types ?? []).includes('Files') || data.getData('text/uri-list') !== ''
  }

  function onDragEnter(event: DragEvent): void {
    if (!hasFiles(event.dataTransfer)) return
    event.preventDefault()
    depth += 1
    active.value = true
  }

  function onDragOver(event: DragEvent): void {
    if (!hasFiles(event.dataTransfer)) return
    // 不拦这一下，浏览器就不允许落下来放东西
    event.preventDefault()
    if (event.dataTransfer !== null) event.dataTransfer.dropEffect = 'copy'
  }

  function onDragLeave(): void {
    depth -= 1
    if (depth <= 0) {
      depth = 0
      active.value = false
    }
  }

  /** 拖的是文字、而且落点在输入框里：那是「把这段字挪个位置」，不该被我们接走 */
  function isTextIntoEditable(event: DragEvent): boolean {
    if (hasFiles(event.dataTransfer)) return false
    const target = event.target
    if (!(target instanceof Element)) return false
    return target.closest('input, textarea, [contenteditable="true"]') !== null
  }

  function onDrop(event: DragEvent): void {
    if (isTextIntoEditable(event)) return
    // 无论认不认得这些路径，都得拦下默认行为：否则 WebView2 会把窗口导航走
    event.preventDefault()
    depth = 0
    active.value = false
    const paths = pathsFrom(event.dataTransfer)
    if (paths.length > 0) handlers.onPaths(paths)
    else handlers.onUnsupported(Array.from(event.dataTransfer?.files ?? []).length > 0)
  }

  type NativeDropBridge = Window & {
    /** 原生侧拿到拖入的路径后调用（文件、多个文件、文件夹都一样）。 */
    __markpicDrop?: (paths: unknown) => void
    /** 原生侧拖入 / 离开时调用，用来点亮整窗的放置态。 */
    __markpicDragOverlay?: (on: boolean) => void
  }

  onMounted(() => {
    const bridge = window as NativeDropBridge
    bridge.__markpicDrop = (paths) => {
      if (!Array.isArray(paths)) return
      const clean: string[] = []
      const seen = new Set<string>()
      for (const item of paths) {
        if (typeof item !== 'string') continue
        const path = item.trim()
        if (path === '' || seen.has(path.toLowerCase())) continue
        seen.add(path.toLowerCase())
        clean.push(path)
      }
      if (clean.length > 0) handlers.onPaths(clean)
    }
    bridge.__markpicDragOverlay = (on) => {
      active.value = on
    }

    window.addEventListener('dragenter', onDragEnter)
    window.addEventListener('dragover', onDragOver)
    window.addEventListener('dragleave', onDragLeave)
    window.addEventListener('drop', onDrop)
  })

  onBeforeUnmount(() => {
    const bridge = window as NativeDropBridge
    bridge.__markpicDrop = undefined
    bridge.__markpicDragOverlay = undefined

    window.removeEventListener('dragenter', onDragEnter)
    window.removeEventListener('dragover', onDragOver)
    window.removeEventListener('dragleave', onDragLeave)
    window.removeEventListener('drop', onDrop)
  })

  return { active }
}