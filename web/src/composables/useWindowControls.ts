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
   * 把标题栏里「双击可最大化」的范围告诉后端（CSS 像素）：标题栏高度、
   * 顶部缩放热区的厚度、右侧按钮区从右边量过来有多宽。
   *
   * 后端据此在 WebView2 的渲染子窗口上判双击。返回值是「原生判定挂上了没有」：
   * true 时网页这边就不要再自己判了，两条路同时报会互相抵消（见 DOUBLE_CLICK_MS）。
   */
  cmpWinCaption?: (bar: number, top: number, right: number) => Promise<boolean>
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

/** 窗口四边的缩放热区厚度（和 App.vue 里 .rz__zone 一致），双击落在里面不算标题栏。 */
const RESIZE_ZONE = 5

/**
 * 双击标题栏最大化由后端在原生层判（cmd/markpic/caption_windows.go）：
 * 在 WebView2 的渲染子窗口上收 WM_LBUTTONDOWN，按 800ms / 20 物理像素配对。
 *
 * 下面这套网页判定只是兜底 —— 万一原生那条没挂上（找不到渲染子窗口），
 * 由它接住，否则双击就彻底没了。两条同时生效反而会互相抵消：
 * 第二下按下这里先切一次，抬起之后浏览器再发一次 dblclick，慢一点的点击
 * 就超过防抖间隔，「最大化 + 还原」两下抵消 —— 用户看到的正是「毫无反应」。
 */
const DOUBLE_CLICK_MS = 500
const DOUBLE_CLICK_SLOP = 8

/** 兜底判定里两次切换之间的最小间隔。 */
const TOGGLE_GUARD_MS = 180

/** 原生判定是否已经接管（由 cmpWinCaption 的返回值告知）。 */
let nativeCaption = true

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

/**
 * 把标题栏里「双击可最大化」的范围报给后端，让原生判定知道哪一块算标题栏。
 *
 * 参数都是 CSS 像素：标题栏高度、顶部缩放热区的厚度、右侧按钮区从右边量过来
 * 有多宽（按钮归按钮自己，双击它们不该切窗口）。后端按窗口 DPI 折算成物理像素。
 * 返回值是原生判定挂上了没有：挂上了就把本页这条兜底判定关掉。
 */
function reportCaptionBand(): void {
  if (!available.value) return
  const bar = document.querySelector('.tb')
  if (!(bar instanceof HTMLElement)) return
  const tools = document.querySelector('.tb__tools')
  const height = Math.round(bar.getBoundingClientRect().height)
  const right = tools instanceof HTMLElement ? Math.round(window.innerWidth - tools.getBoundingClientRect().left) : 0
  if (typeof bridge.cmpWinCaption !== 'function') {
    nativeCaption = false
    return
  }
  void Promise.resolve(bridge.cmpWinCaption(height, RESIZE_ZONE, right))
    .then(attached => {
      nativeCaption = attached === true
    })
    .catch(() => {
      nativeCaption = false
    })
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

  const { resolved } = useTheme()
  // 窗口边框、标题栏明暗跟着主题走，否则深色界面配一圈亮边框。
  watch(
    resolved,
    value => {
      void bridge.cmpWinSetDark?.(value === 'dark')
    },
    { immediate: true },
  )

  // 热区上的事件由后端推回来（window_windows.go 里的 setSnapOverlayEvents）。
  bridge.__markpicSnap = handleSnapEvent

  window.addEventListener('resize', () => {
    if (!maximizeInFlight) void syncMaximized()
    // 窗口大小变了，最大化按钮在屏幕上的位置也跟着变。
    reportSnapZone()
    reportCaptionBand()
  })

  // 标题栏右侧那排按钮的宽度会随窗口宽度、语言和连接状态变，原生判定要知道
  // 它从哪儿开始、不能碰，所以盯着这块元素的尺寸。
  const tools = document.querySelector('.tb__tools')
  if (tools instanceof HTMLElement && typeof ResizeObserver !== 'undefined') {
    new ResizeObserver(reportCaptionBand).observe(tools)
  }

  void syncMaximized()
  // 布局落定之后再量：标题栏高度和按钮位置都要等第一帧画完才准。
  void nextFrame().then(() => reportCaptionBand())
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
     * 在标题栏空白处按下。真正的交接推迟到鼠标确实移动之后：
     * 只是点一下的话，这次点击留给原生那条双击判定（见 DOUBLE_CLICK_MS 的注释）；
     * 拖动的话，一到阈值就把「按下的那一点」交给系统的标题栏拖动循环，
     * 吸附、双击还原之类的原生手感都还在。
     */
    startDrag: (event: MouseEvent): void => {
      if (!available.value || event.button !== 0) return
      const target = event.target as HTMLElement | null
      if (target?.closest('[data-no-drag]')) return
      event.preventDefault()

      // 兜底的双击判定（原生那条没挂上才走到这里）：两次按下挨得够近就是双击。
      // 判完把配对清掉，免得第三次点击又跟第二次配成一对。
      if (!nativeCaption) {
        const now = Date.now()
        const repeat =
          now - lastDownAt <= DOUBLE_CLICK_MS &&
          Math.abs(event.clientX - lastDownX) <= DOUBLE_CLICK_SLOP &&
          Math.abs(event.clientY - lastDownY) <= DOUBLE_CLICK_SLOP
        lastDownAt = repeat ? 0 : now
        lastDownX = event.clientX
        lastDownY = event.clientY
        if (repeat) {
          toggleMaximize()
          return
        }
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
        // 这里故意不清 lastDownAt：越过阈值往往只是手抖，第二下马上就到，
        // 清掉配对等于把双击判死。真正的拖动持续几百毫秒，松手后再单击时
        // 早已超过 DOUBLE_CLICK_MS，不会误判成双击。
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
