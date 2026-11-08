import { computed, onMounted, ref, watch } from 'vue'
import { useTheme } from './useTheme'

/** 可拖拽缩放的窗口边。 */
export type ResizeEdge =
  | 'top'
  | 'right'
  | 'bottom'
  | 'left'
  | 'top-left'
  | 'top-right'
  | 'bottom-left'
  | 'bottom-right'

/**
 * 桌面窗口命令由后端在 WebView2 里注入（cmd/convert-md-picture/window_windows.go）。
 * 窗口没有系统标题栏，最小化 / 最大化 / 关闭、拖动和缩放都得由界面这里的
 * 交互回调过去，再交给系统自己的窗口循环处理。
 * 在浏览器里打开同一个界面时这些函数不存在，相关按钮自动隐藏。
 */
interface WindowBridge {
  cmpWinMinimize?: () => Promise<unknown>
  cmpWinToggleMaximize?: () => Promise<unknown>
  cmpWinMaximized?: () => Promise<boolean>
  cmpWinClose?: () => Promise<unknown>
  cmpWinDrag?: () => Promise<unknown>
  cmpWinResize?: (edge: ResizeEdge) => Promise<unknown>
  cmpWinSetDark?: (dark: boolean) => Promise<unknown>
}

const bridge = window as unknown as WindowBridge

const available = ref(false)
const maximized = ref(false)

let started = false
let maximizeInFlight = false

async function syncMaximized(): Promise<void> {
  if (typeof bridge.cmpWinMaximized !== 'function') return
  try {
    const value = await bridge.cmpWinMaximized()
    maximized.value = value === true
  } catch {
    // 窗口可能正在关闭，读不到就保持上一次的状态
  }
}

/** 模块级只启动一次：两个组件都会用到这套命令，监听不能重复挂。 */
function start(): void {
  if (started) return
  started = true

  available.value = typeof bridge.cmpWinDrag === 'function'
  if (!available.value) return

  const { resolved } = useTheme()
  // 窗口边框、标题栏明暗跟着主题走，否则深色界面配一圈亮边框。
  watch(
    resolved,
    value => {
      void bridge.cmpWinSetDark?.(value === 'dark')
    },
    { immediate: true },
  )

  window.addEventListener('resize', () => {
    if (!maximizeInFlight) void syncMaximized()
  })

  void syncMaximized()
}

export function useWindowControls() {
  onMounted(start)

  return {
    /** 当前是不是在桌面窗口里跑（浏览器里为 false）。 */
    available: computed(() => available.value),
    maximized: computed(() => maximized.value),
    minimize: (): void => {
      void bridge.cmpWinMinimize?.()
    },
    toggleMaximize: (): void => {
      maximizeInFlight = true
      void Promise.resolve(bridge.cmpWinToggleMaximize?.())
        .then(() => new Promise(resolve => window.setTimeout(resolve, 60)))
        .then(() => syncMaximized())
        .finally(() => {
          maximizeInFlight = false
        })
    },
    close: (): void => {
      void bridge.cmpWinClose?.()
    },
    /** 在标题栏空白处按下：把这次按下交给系统去拖动窗口。 */
    startDrag: (event: MouseEvent): void => {
      if (!available.value || event.button !== 0) return
      const target = event.target as HTMLElement | null
      if (target?.closest('[data-no-drag]')) return
      event.preventDefault()
      void bridge.cmpWinDrag?.()
    },
    /** 在窗口边缘按下：把这次按下交给系统去缩放窗口。 */
    startResize: (edge: ResizeEdge): void => {
      if (!available.value) return
      void bridge.cmpWinResize?.(edge)
    },
  }
}
