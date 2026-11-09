import { onBeforeUnmount, onMounted, ref } from 'vue'

/**
 * 把文件/目录拖进窗口就等于把它们加进队列。
 *
 * 两件事都不能省：
 *  1. dragover/drop 一定要 preventDefault —— 不拦的话 WebView2 会按浏览器默认行为
 *     直接导航到那个文件（file:///…），界面当场没了，只能重启。
 *  2. 拿到路径。Chromium 系里 `File` 不带本地路径，但拖文件时 `text/uri-list`
 *     往往就是 `file:///C:/…`；拖一段文字（比如从记事本里选中的路径）落在
 *     `text/plain` 里，那就当路径列表处理。都拿不到时返回空数组，由调用方说明情况。
 */
export interface FileDropHandlers {
  onPaths: (paths: string[]) => void
  /** 拖进来了，但拿不到本地路径 */
  onUnsupported: () => void
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

  function onDrop(event: DragEvent): void {
    // 无论认不认得这些路径，都得拦下默认行为：否则 WebView2 会把窗口导航走
    event.preventDefault()
    depth = 0
    active.value = false
    const paths = pathsFrom(event.dataTransfer)
    if (paths.length > 0) handlers.onPaths(paths)
    else handlers.onUnsupported()
  }

  onMounted(() => {
    window.addEventListener('dragenter', onDragEnter)
    window.addEventListener('dragover', onDragOver)
    window.addEventListener('dragleave', onDragLeave)
    window.addEventListener('drop', onDrop)
  })

  onBeforeUnmount(() => {
    window.removeEventListener('dragenter', onDragEnter)
    window.removeEventListener('dragover', onDragOver)
    window.removeEventListener('dragleave', onDragLeave)
    window.removeEventListener('drop', onDrop)
  })

  return { active }
}