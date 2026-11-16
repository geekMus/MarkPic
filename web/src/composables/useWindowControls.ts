import { computed, onMounted, ref, watch } from 'vue'
import { useTheme } from './useTheme'
import { markResizing } from './useResizing'

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
 * 桌面窗口命令由后端在 WebView2 里注入（cmd/markpic/window_windows.go）。
 * 窗口没有系统标题栏，最小化 / 最大化 / 关闭、拖动和缩放都得由界面这里的
 * 交互回调过去，再交给系统自己的窗口循环处理。
 * 在浏览器里打开同一个界面时这些函数不存在，相关按钮自动隐藏。
 */
interface WindowBridge {
  cmpWinMinimize?: () => Promise<unknown>
  cmpWinToggleMaximize?: () => Promise<unknown>
  cmpWinMaximized?: () => Promise<boolean>
  cmpWinClose?: () => Promise<unknown>
  /** 收进系统托盘（不退出）；返回 false 表示托盘图标没建起来。 */
  cmpWinTray?: () => Promise<boolean>
  /** 已经确认退出：先取消任务，再绕过关闭确认真正关窗。 */
  cmpWinQuit?: () => Promise<unknown>
  cmpWinDrag?: (x: number, y: number) => Promise<unknown>
  cmpWinResize?: (edge: ResizeEdge) => Promise<unknown>
  cmpWinSetDark?: (dark: boolean) => Promise<unknown>
  /** 把用户选的主题（system / light / dark）交给后端存下来，下次启动先用对底色。 */
  cmpWinTheme?: (choice: string) => Promise<unknown>
  cmpWinFlash?: () => Promise<unknown>
  /**
   * 把最大化按钮的位置交给后端，在它上面盖一块原生热区。
   *
   * Win11 的贴靠布局浮层只认系统自己的「最大化按钮」——客户区被 WebView2
   * 铺满之后系统根本看不到我们画的那个 <button>，得靠一块真的原生窗口顶上去
   * （见 cmd/markpic/snap_windows.go）。参数是 (右边缘离客户区右边多远, 上边,
   * 宽, 高)，单位 CSS 像素；宽或高传 0 表示把热区撤掉。
   */
  cmpWinSnapZone?: (right: number, top: number, width: number, height: number) => Promise<unknown>
  /**
   * 热区上的鼠标进出 / 按下 / 抬起由后端推回来。那块像素被原生窗口盖住，
   * 网页自己收不到 hover 和 click，只能这样接回来。
   */
  __markpicSnap?: (kind: SnapEvent) => void
}

/**
 * 原生热区回报的事件。
 *
 * click 由后端判定：光标抬起时还压在热区上才算一次点击。前端看不到那块
 * 原生窗口，光靠 up 判断不了「按住拖出去再松手」这种该作废的情况。
 */
type SnapEvent = 'enter' | 'leave' | 'down' | 'up' | 'click'

/**
 * 拖动标题栏时，鼠标移动超过这个距离（CSS 像素）才把这次按下交给系统。
 * 阈值不能是 0：一旦交给系统，鼠标就被拖动循环接管，网页再也收不到
 * 这次抬起，浏览器也就凑不出 click / dblclick。
 *
 * 给到 10 而不是 4：手抖、触摸板轻点会让按下之后漂几个像素，阈值太小会把
 * 「其实只是想双击」的第一下当成拖动交出去，手感也发涩。
 */
const DRAG_THRESHOLD = 10

/**
 * 双击标题栏的判定。两次按下间隔 ≤800ms、屏幕距离 ≤20 CSS 像素算双击；
 * 第二下按下后不立刻切，等抬起时确认这一下没有变成拖动再切。
 */
const DOUBLE_CLICK_MS = 800
const DOUBLE_CLICK_SLOP = 20

/** 两次切换之间的最小间隔。 */
const TOGGLE_GUARD_MS = 180

let lastDownAt = 0
let lastDownX = 0
let lastDownY = 0
let lastToggleAt = 0

const bridge = window as unknown as WindowBridge

const available = ref(false)
const maximized = ref(false)
/** 鼠标正停在最大化键的原生热区上（那块像素归原生窗口，CSS :hover 收不到）。 */
const snapHover = ref(false)
const snapDown = ref(false)

let started = false
let maximizeInFlight = false
let maxButton: HTMLElement | null = null

/** 等到浏览器画完这一帧：量元素矩形之前先让布局落定。 */
function nextFrame(): Promise<void> {
  return new Promise(resolve => window.requestAnimationFrame(() => resolve()))
}

async function syncMaximized(): Promise<void> {
  if (typeof bridge.cmpWinMaximized !== 'function') return
  try {
    const value = await bridge.cmpWinMaximized()
    maximized.value = value === true
  } catch {
    // 窗口可能正在关闭，读不到就保持上一次的状态
  }
}

/**
 * 把最大化按钮的矩形报给后端，让那块原生热区盖在它上面。
 *
 * 坐标用 getBoundingClientRect 那一套（相对客户区左上角、CSS 像素），后端
 * 再按窗口 DPI 折算成物理像素 —— 前端不用关心缩放。
 * 右侧用「右边缘离右边多远」而不是 left：按钮贴右排，窗口变宽时这个值不变。
 */
function reportSnapZone(): void {
  if (typeof bridge.cmpWinSnapZone !== 'function') return
  const el = maxButton
  if (!available.value || !el) {
    void bridge.cmpWinSnapZone(0, 0, 0, 0)
    return
  }
  const rect = el.getBoundingClientRect()
  const right = Math.round(window.innerWidth - rect.right)
  const top = Math.round(rect.top)
  const width = Math.round(rect.width)
  const height = Math.round(rect.height)
  void bridge.cmpWinSnapZone(right, top, width, height)
}

function toggleMaximize(): void {
  const now = Date.now()
  if (now - lastToggleAt < TOGGLE_GUARD_MS) return
  lastToggleAt = now
  maximizeInFlight = true
  void Promise.resolve(bridge.cmpWinToggleMaximize?.())
    .then(() => new Promise(resolve => window.setTimeout(resolve, 60)))
    .then(() => syncMaximized())
    .finally(() => {
      maximizeInFlight = false
      // 最大化之后标题栏的可拖拽区没了、按钮换了图标，但位置其实不变；
      // 顺手再报一次，缩放比例变了的显示器上也能跟着对齐。
      reportSnapZone()
    })
}

/**
 * 原生热区上的鼠标事件。那块像素被原生窗口占着，网页收不到 :hover 和 click，
 * 悬停高亮和「点了最大化」都得从这里补回来。
 */
function handleSnapEvent(kind: SnapEvent): void {
  switch (kind) {
    case 'enter':
      snapHover.value = true
      break
    case 'leave':
      snapHover.value = false
      snapDown.value = false
      break
    case 'down':
      snapDown.value = true
      break
    case 'up':
      // 只清按下态；要不要最大化由后面的 click 说了算。
      snapDown.value = false
      break
    case 'click':
      snapDown.value = false
      toggleMaximize()
      break
  }
}

/** 模块级只启动一次：两个组件都会用到这套命令，监听不能重复挂。 */
function start(): void {
  if (started) return
  started = true

  available.value = typeof bridge.cmpWinDrag === 'function'
  if (!available.value) return

  const { choice, resolved } = useTheme()
  // 窗口边框、标题栏明暗跟着主题走，否则深色界面配一圈亮边框。
  watch(
    resolved,
    value => {
      void bridge.cmpWinSetDark?.(value === 'dark')
    },
    { immediate: true },
  )
  // 用户选的是「跟随系统」，这个选择本身也要让后端记住：下次启动窗口刚出现、
  // 界面还没挂上的那几十毫秒，原生底色得按同一个选择来填，不能只记明暗。
  watch(
    choice,
    value => {
      void bridge.cmpWinTheme?.(value)
    },
    { immediate: true },
  )

  // 热区上的事件由后端推回来（window_windows.go 里的 setSnapOverlayEvents）。
  bridge.__markpicSnap = handleSnapEvent

  window.addEventListener('resize', () => {
    if (!maximizeInFlight) void syncMaximized()
    // 窗口大小变了，最大化按钮在屏幕上的位置也跟着变。
    reportSnapZone()
  })

  void syncMaximized()
}

export function useWindowControls() {
  onMounted(start)

  return {
    /** 当前是不是在桌面窗口里跑（浏览器里为 false）。 */
    available: computed(() => available.value),
    maximized: computed(() => maximized.value),
    /** 鼠标停在那块原生热区上（最大化键的悬停态靠它，CSS :hover 收不到）。 */
    snapHover: computed(() => snapHover.value),
    /** 正在热区上按着不放（用来画按下态，和 :active 一致）。 */
    snapDown: computed(() => snapDown.value),
    /**
     * 注册最大化按钮的 DOM 元素：随后的 resize 与挂载都会按它的矩形摆热区。
     * 传 null 表示按钮不在了（例如窄版把它藏了起来），热区一并撤掉。
     */
    setSnapTarget: (el: HTMLElement | null): void => {
      maxButton = el
      if (el) {
        // 布局刚变过，等这一帧画完再量，拿到的才是最终矩形。
        void nextFrame().then(() => reportSnapZone())
      } else {
        reportSnapZone()
      }
    },
    minimize: (): void => {
      void bridge.cmpWinMinimize?.()
    },
    toggleMaximize,
    close: (): void => {
      void bridge.cmpWinClose?.()
    },
    /** 收进系统托盘；没有托盘命令或挂图标失败时返回 false。 */
    tray: async (): Promise<boolean> => {
      if (typeof bridge.cmpWinTray !== 'function') return false
      try {
        return (await bridge.cmpWinTray()) === true
      } catch {
        return false
      }
    },
    /** 关闭确认里点了退出：后端会先取消所有任务，再放行窗口关闭。 */
    quit: (): void => {
      void bridge.cmpWinQuit?.()
    },
    /**
     * 在标题栏空白处按下。单击先记下来；拖动超过阈值才把这一下交给系统的
     * 标题栏拖动循环（吸附、拖动最大化窗口时还原由系统负责）。
     */
    startDrag: (event: MouseEvent): void => {
      if (!available.value || event.button !== 0) return
      const target = event.target as HTMLElement | null
      if (target?.closest('[data-no-drag]')) return
      event.preventDefault()

      // 两次按下挨得够近就是双击。判完把配对清掉，免得第三次点击又跟第二次一对。
      const now = Date.now()
      const repeat =
        now - lastDownAt <= DOUBLE_CLICK_MS &&
        Math.abs(event.clientX - lastDownX) <= DOUBLE_CLICK_SLOP &&
        Math.abs(event.clientY - lastDownY) <= DOUBLE_CLICK_SLOP
      lastDownAt = repeat ? 0 : now
      lastDownX = event.clientX
      lastDownY = event.clientY
      if (repeat) {
        // 第二下不立刻切：先等抬起。若按住拖过阈值，就按拖动处理。
        const secondX = event.screenX
        const secondY = event.screenY
        let dragging = false
        const stopRepeat = (): void => {
          window.removeEventListener('mousemove', onRepeatMove, true)
          window.removeEventListener('mouseup', onRepeatUp, true)
        }
        const onRepeatMove = (move: MouseEvent): void => {
          if (
            Math.abs(move.screenX - secondX) < DRAG_THRESHOLD &&
            Math.abs(move.screenY - secondY) < DRAG_THRESHOLD
          ) {
            return
          }
          dragging = true
          stopRepeat()
          void bridge.cmpWinDrag?.(secondX, secondY)
        }
        const onRepeatUp = (): void => {
          stopRepeat()
          if (!dragging) toggleMaximize()
        }
        window.addEventListener('mousemove', onRepeatMove, true)
        window.addEventListener('mouseup', onRepeatUp, true)
        return
      }

      const startX = event.screenX
      const startY = event.screenY

      const stop = (): void => {
        window.removeEventListener('mousemove', onMove, true)
        window.removeEventListener('mouseup', stop, true)
      }

      const onMove = (move: MouseEvent): void => {
        if (Math.abs(move.screenX - startX) < DRAG_THRESHOLD && Math.abs(move.screenY - startY) < DRAG_THRESHOLD) {
          return
        }
        stop()
        // 越过阈值就是拖动，不是双击：把配对清掉，免得松手后马上单击又被
        // 当成第二下（窗口已经被拖走，屏幕坐标却可能还在 20px 内）。
        lastDownAt = 0
        void bridge.cmpWinDrag?.(startX, startY)
      }

      window.addEventListener('mousemove', onMove, true)
      window.addEventListener('mouseup', stop, true)
    },
    /** 在窗口边缘按下：把这次按下交给系统去缩放窗口。 */
    startResize: (edge: ResizeEdge): void => {
      if (!available.value) return
      // 缩放循环一进来就会连续重排，先按「正在缩放」把贵的效果关掉
      markResizing()
      void bridge.cmpWinResize?.(edge)
    },
  }
}
