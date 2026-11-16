<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import AboutDialog from './components/AboutDialog.vue'
import AppFooter from './components/AppFooter.vue'
import AppHeader from './components/AppHeader.vue'
import AppIcon from './components/AppIcon.vue'
import AppRail from './components/AppRail.vue'
import CheckPage from './components/CheckPage.vue'
import CloseConfirmDialog from './components/CloseConfirmDialog.vue'
import DownloadPage from './components/DownloadPage.vue'
import FileQueue from './components/FileQueue.vue'
import OptionsPanel from './components/OptionsPanel.vue'
import ToastStack from './components/ToastStack.vue'
import {
  describeError,
  isDialogUnsupported,
  pickFiles,
  pickFolder,
  revealPath,
} from './api'
import { useFileDrop } from './composables/useFileDrop'
import { useHealth } from './composables/useHealth'
import {
  cancelRowScan,
  currentQueuePage,
  queuePagePaths,
  queuePagePendingPreview,
  restoreQueuePage,
  retryRowScan,
  scanPaths,
  setQueuePage,
  useRun,
} from './composables/useRun'
import { useSettings } from './composables/useSettings'
import { useTheme } from './composables/useTheme'
import { useToasts } from './composables/useToasts'
import { useUpdate } from './composables/useUpdate'
import { useWindowControls } from './composables/useWindowControls'
import { useI18n, type MessageKey } from './i18n'
import { JOB_VIEWS, modeOfView, viewOfMode, type JobView, type RemoteMode, type View } from './types'

const queueRef = ref<InstanceType<typeof FileQueue> | null>(null)

const { toasts, dismiss, warning, success, info } = useToasts()
const { t } = useI18n()
const { resolved: theme, cycle: toggleTheme } = useTheme()
// 窗口没有系统边框，缩放热区得由界面在四条边上铺出来
const { available: windowChrome, minimize, tray, quit, startResize } = useWindowControls()
const { health, loading: healthLoading, refresh: refreshHealth } = useHealth()

const {
  draft: settings,
  dirty,
  loaded: settingsLoaded,
  loading: settingsLoading,
  saving: settingsSaving,
  loadError: settingsError,
  load: loadSettings,
  save: saveSettings,
  rememberCloseAction,
  update: updateSettings,
  discard: discardSettings,
  snapshot: settingsSnapshot,
} = useSettings()

const {
  phase,
  rowViews,
  stats,
  busy,
  runningPages,
  isRunBusy,
  totalCount,
  completedCount,
  revealTarget,
  connect,
  disconnect,
  addPaths,
  start,
  cancel,
  removeRow,
  clearQueue,
} = useRun()

// 主视图 = 侧边栏上的菜单项。左边三项是作业视图（共用一条队列，区别只在远程图
// 怎么处理），右边两项是表单页（结果写到哪 / 怎么跑）。设置不再占用弹窗。
//
// 关掉再打开要回到上次停留的那一页：队列本来就是按页存的，回来时接着看。
const VIEW_KEY = 'markpic.view'
const SAVED_VIEWS: readonly View[] = [...JOB_VIEWS, 'settings']

function readView(): View {
  try {
    const raw = window.localStorage.getItem(VIEW_KEY)
    if (raw !== null && (SAVED_VIEWS as readonly string[]).includes(raw)) return raw as View
  } catch {
    /* 存不了就当没存过 */
  }
  return 'convert'
}

const view = ref<View>(readView())

/** 作业视图对应的远程图处理方式；表单页没有这个属性。 */
function jobModeOf(next: View): RemoteMode | null {
  return JOB_VIEWS.includes(next as JobView) ? modeOfView(next) : null
}

/**
 * 这一轮真正会用到的远程图模式。表单页上沿用最后一次的作业视图 ——
 * 否则在「输出」里改完设置、回队列一开跑，模式就被悄悄重置成「转换」了。
 */
const mode = ref<RemoteMode>('ignore')
const isJobView = computed(() => jobModeOf(view.value) !== null)
const isFormView = computed(() => !isJobView.value)

/**
 * 三个作业页各有一份队列（见 useRun 里的 QueuePage）。
 *
 * 切菜单 = 换一页：转换里的文件、结果、进度留在转换，下载那一页还是它自己的样子；
 * 切回来接着看。跑着的那一轮不跟着画面走，它留在开始它的那一页上。
 */
const JOB_MODES: readonly RemoteMode[] = ['ignore', 'download', 'check']

watch(
  view,
  (next) => {
    try {
      localStorage.setItem(VIEW_KEY, next)
    } catch {
      /* 存不下不影响切换 */
    }
    const nextMode = jobModeOf(next)
    if (nextMode === null) return
    setQueuePage(nextMode)
    // 切到哪一页，就补扫那一页还没扫描过的行（启动时只扫了当时停留的那一页）
    scanPage(nextMode)
  },
  { immediate: true },
)

/** 哪些作业页正在跑：侧边栏每个跑着的按钮上都点个记号，别让人以为作业丢了。 */
const runningViews = computed<View[]>(() => runningPages.value.map(viewOfMode))

/** 当前这一页是不是正在跑：页头细线和底栏按钮只看这一页。 */
const jobBusy = computed(() => isRunBusy(mode.value))

/**
 * 这一轮的总进度（0..1），给页头那条细线和百分比用。
 *
 * 按「真正要处理的单元」算，而不是按文件数：一轮只有一个文件时文件级进度永远
 * 停在 0；按全部图片数算又会把检测/下载模式下根本不碰的本地图也算进分母。
 * 每个行上的 workTotal 是该文件本模式的去重单元数（上传=本地图，检测/下载=远程
 * 地址），file.progress 一到就由服务端换成权威值。一个单元都没有的文件不进分母；
 * 有单元的文件跑完后 workDone 等于 workTotal，分子分母一起计入，进度条不会卡在中间。
 */
const jobProgress = computed(() => {
  let done = 0
  let total = 0
  for (const row of rowViews.value) {
    const units = row.workTotal ?? 0
    if (units <= 0) continue
    total += units
    done += Math.min(row.workDone ?? 0, units)
  }
  if (total > 0) return Math.max(0, Math.min(1, done / total))
  // 还没有任何单元数（例如刚点开始、第一个事件没到）：先按文件完成度垫着。
  return totalCount.value > 0 ? completedCount.value / totalCount.value : 0
})

/** 别页在跑时，底栏说一句是哪几页：三页能同时跑，但别处的活也该有个交代。 */
const runningElsewhere = computed<string>(() => {
  const others = runningPages.value.filter(pageMode => pageMode !== mode.value)
  if (others.length === 0) return ''
  const names = others.map(pageMode => t(`nav.${viewOfMode(pageMode)}` as MessageKey))
  if (names.length === 1) return t('footer.runningElsewhere', { page: names[0] })
  return t('footer.runningElsewhereMany', { pages: names.join(t('common.listJoin')) })
})
const aboutOpen = ref(false)

/**
 * 点叉号需要确认的两种情况（见 cmd/markpic/chrome_windows.go 的 WM_CLOSE）：
 * - 没有任务、设置是「每次询问」：选「最小化」还是「退出程序」，可勾「不再询问」，
 *   勾了就按这次点的那个动作记住，以后点叉号直接照做、不再弹。
 * - 还有任务：只确认是否退出，正文写清任务数；永远不给「不再询问」。
 */
interface CloseRequestWindow extends Window {
  __markpicCloseRequest?: (count: number, ask?: boolean) => void
}
const closeBridge = window as CloseRequestWindow
const closePrompt = ref(false)
const closePromptCount = ref(0)
const closePromptAsk = ref(false)

function handleCloseRequest(count: number, ask = false): void {
  closePromptCount.value = Number.isFinite(count) ? Math.max(0, Math.trunc(count)) : 0
  // 有任务时只做退出确认；后端已经保证这一点，这里再兜一层，免得旧消息带出「不再询问」。
  closePromptAsk.value = ask === true && closePromptCount.value === 0
  closePrompt.value = true
}

function dismissClosePrompt(): void {
  closePrompt.value = false
}

// 「最小化到托盘」：窗口从任务栏消失，进程和任务都还在。
// 勾了「不再询问」就把这次的选择记下来，以后点叉号直接收进托盘。
async function minimizeClosePrompt(remember: boolean): Promise<void> {
  closePrompt.value = false
  if (!(await tray())) {
    // 托盘图标没建起来：明确说一声，然后退回普通最小化，至少窗口还在
    warning(t('toast.trayFailed'))
    minimize()
    return
  }
  if (remember) await rememberCloseAction('tray')
}

// 「退出程序」：勾了「不再询问」就记住直接退出，然后结束进程。
async function quitClosePrompt(remember: boolean): Promise<void> {
  closePrompt.value = false
  if (remember) await rememberCloseAction('exit')
  quit()
}

// 弹窗开着的时候任务状态变了，这一问就过期了：
// 任务跑起来时「没有任务」的前提不成立，任务跑完时写下的数字又不对了。
// 收起弹窗，等下一次关窗再按新的任务数重新问。
watch(busy, () => {
  if (closePrompt.value) closePrompt.value = false
})
// 选中哪一行：只用来给那一行描边，明细由行自己展开。
const selectedId = ref<string | null>(null)

function selectRow(id: string): void {
  selectedId.value = id
}

const { hasUpdate, autoCheck, check: checkUpdate, stopPolling } = useUpdate()

const version = computed(() => health.value?.version ?? '')
const picgo = computed(() => health.value?.picgo ?? null)
// 服务端给的 needed 是按**已保存**的设置算的，而面板里的选择可能还没保存就开跑
// （整轮用的就是面板里这份）。这盏灯回答的是「现在能不能开始」，所以按面板里
// 真正会用的那份设置来判：只检测 / 只下载 / 调试构建都不上传。
const picgoNeeded = computed(() => mode.value === 'ignore' && !debug.value)
const dataDir = computed(() => health.value?.dataDir ?? '')
const dataNote = computed(() => health.value?.dataNote ?? '')
const settingsNote = computed(() => health.value?.settingsNote ?? '')
/**
 * 调试构建（npm run start --debug 编的那一版）：整轮只扫描、不写文件、不上传。
 *
 * 默认开发构建与发布版一致：health.debug 恒为 false，界面据此不渲染任何
 * 「调试模式」字样 —— 它不是一个用户设置。
 */
const debug = computed(() => health.value?.debug === true)

/**
 * 扫描没通过的行：正在扫、超时、被取消、找不到文件。
 *
 * 这些行参与不了这一轮 —— 图片数和远程地址都是跑之前就要算清的东西，
 * 带着一份没扫通的文件去跑，只会跑到一半才发现数不对。
 */
const scanBlocked = computed(() => rowViews.value.filter((row) => (row.scan ?? 'idle') !== 'ok'))

/** 正在扫的行数：底栏据此说「正在扫描 2 个文件…」，而不是干灰着按钮。 */
const scanningCount = computed(
  () => rowViews.value.filter((row) => (row.scan ?? 'idle') === 'scanning').length,
)

// 全都扫通了才允许开始。强行点击也进不来：handleRun 还要再判一次 canStart。
const canStart = computed(
  () => !isRunBusy(mode.value) && rowViews.value.length > 0 && scanBlocked.value.length === 0,
)

/** 按钮为什么是灰的：给底栏一句话，别让人对着禁用按钮猜。 */
const startHint = computed(() => {
  if (isRunBusy(mode.value)) return ''
  if (rowViews.value.length === 0) return ''
  if (scanningCount.value > 0) return t('footer.blocked.scanning', { count: scanningCount.value })
  if (scanBlocked.value.length > 0) return t('footer.blocked.problem', { count: scanBlocked.value.length })
  return ''
})
const canReveal = computed(() => revealTarget.value !== null)

async function pick(kind: 'files' | 'folder'): Promise<void> {
  try {
    const paths = kind === 'files' ? await pickFiles() : await pickFolder()
    if (paths.length === 0) return
    await enqueue(paths)
  } catch (error) {
    if (isDialogUnsupported(error)) {
      warning(t('toast.dialogFallback'))
      return
    }
    warning(t('error.pickPath', { detail: describeError(error, t('error.unknown')) }))
  }
}

/* 最近用过的路径：只记最后六条，队列为空时摆在空状态里点一下就回来。 */
const RECENT_KEY = 'markpic.recent'
const RECENT_MAX = 6

function readRecent(): string[] {
  try {
    const raw = JSON.parse(localStorage.getItem(RECENT_KEY) ?? '[]')
    return Array.isArray(raw) ? raw.filter((item): item is string => typeof item === 'string').slice(0, RECENT_MAX) : []
  } catch {
    return []
  }
}

const recentPaths = ref<string[]>(readRecent())

function rememberPaths(paths: string[]): void {
  // 同一个目录反复拖进来时，路径写法可能差一个尾斜杠或大小写：按归一化后的键去重
  const key = (path: string): string => path.replace(/[\\/]+$/, '').toLowerCase()
  const seen = new Set(paths.map(key))
  const fresh = paths.filter((path, index) => paths.findIndex((other) => key(other) === key(path)) === index)
  const next = [...fresh, ...recentPaths.value.filter((old) => !seen.has(key(old)))].slice(0, RECENT_MAX)
  recentPaths.value = next
  try {
    localStorage.setItem(RECENT_KEY, JSON.stringify(next))
  } catch {
    /* 存不进去就算了，别为了记住路径把转换拦下来 */
  }
}

/* 队列本身也记一份：关掉程序再打开，昨天拖进来的东西还在。
   三页各记各的（转换 / 检测 / 下载），只记路径不记结果 —— 结果要重新扫一遍才算数。 */
const QUEUE_KEY = 'markpic.queue' // 老版本只存一条队列，读一次当转换那一页的
const QUEUES_KEY = 'markpic.queues'

function strings(raw: unknown): string[] {
  return Array.isArray(raw) ? raw.filter((item): item is string => typeof item === 'string') : []
}

function readQueues(): Partial<Record<RemoteMode, string[]>> {
  try {
    const raw = JSON.parse(localStorage.getItem(QUEUES_KEY) ?? 'null') as unknown
    if (raw !== null && typeof raw === 'object' && !Array.isArray(raw)) {
      const out: Partial<Record<RemoteMode, string[]>> = {}
      for (const pageMode of JOB_MODES) {
        out[pageMode] = strings((raw as Record<string, unknown>)[pageMode])
      }
      return out
    }
  } catch {
    /* 存档坏了就当没有，下面再试老键 */
  }
  try {
    const legacy = JSON.parse(localStorage.getItem(QUEUE_KEY) ?? 'null') as unknown
    return { ignore: strings(legacy) }
  } catch {
    return {}
  }
}

function writeQueues(): void {
  try {
    localStorage.setItem(QUEUES_KEY, JSON.stringify(queuePagePaths()))
  } catch {
    /* 存不下就算了，别为了记住路径把转换拦下来 */
  }
}

/**
 * 这一轮要用的设置 = 面板里那份 + 当前菜单决定的远程图模式。
 * 模式不在表单里（它是菜单），所以用覆盖的方式塞进去：既不会让「未保存」变真，
 * 也不会被当成用户改过的设置写回文件。
 */
function jobSettings() {
  return settingsSnapshot({ remote: mode.value })
}

/**
 * 扫一遍某一页里还没扫过的行（切到这一页时顺手做，别让三页在启动时抢网络）。
 *
 * 不等结果：每份文件扫完会自己推事件回来，哪张卡片先扫通哪张先亮。
 */
function scanPage(pageMode: RemoteMode): void {
  const paths = queuePagePendingPreview(pageMode)
  if (paths.length === 0) return
  void scanPaths(pageMode, paths, settingsSnapshot({ remote: pageMode }))
}

/** 启动时把三页各自的队列接回来；正在跑的那一轮由 run.current 负责，别抢。 */
async function restoreQueue(): Promise<void> {
  const saved = readQueues()
  for (const pageMode of JOB_MODES) {
    const paths = saved[pageMode] ?? []
    if (paths.length === 0 || isRunBusy(pageMode)) continue
    restoreQueuePage(pageMode, paths)
  }
  if (!isRunBusy(mode.value)) scanPage(currentQueuePage())
}

async function enqueue(paths: string[]): Promise<void> {
  const added = addPaths(paths)
  if (added.length === 0) {
    if (!isRunBusy(mode.value)) warning(t('toast.pathsQueued'))
    return
  }
  rememberPaths(added)
  // 进队列就开始扫，但不等它：卡片先出来，扫完一份亮一份。
  void scanPaths(mode.value, added, jobSettings())
}

/** 卡片上的「取消扫描」/「恢复扫描」「重试」。 */
function handleCancelScan(id: string): void {
  cancelRowScan(id)
}

function handleRetryScan(id: string): void {
  retryRowScan(id, jobSettings())
}
// 拖进窗口就等于加进队列；拦不住 drag 事件的话 WebView2 会把窗口导航到那个文件
const { active: dropActive } = useFileDrop({
  onPaths: (paths) => void enqueue(paths),
  onUnsupported: (hadFiles) => {
    if (hadFiles) {
      // WebView2 不暴露拖入文件的本地路径，退回系统选择框，让用户点选一次就能进队列
      warning(t('drop.pickFallback'))
      void pick('files')
    } else {
      warning(t('drop.unsupported'))
    }
  },
})

function handleRun(): void {
  if (!canStart.value) return
  void start(
    rowViews.value.map((row) => row.path),
    jobSettings(),
  )
}

async function handleSaveSettings(): Promise<void> {
  if (await saveSettings({ remote: mode.value })) await refreshHealth()
}

/**
 * 手动刷新标题栏的 PicGo 状态。
 *
 * 刷新本身很快，连得上时界面没有任何变化；这里补一句结果，点过之后才知道
 * 那一下真的发生了。请求失败时 useHealth 已经弹过危险提示，不再重复。
 */
async function handleRefreshHealth(): Promise<void> {
  await refreshHealth()
  if (health.value === null) return
  const status = health.value.picgo
  if (status.online) success(t('toast.healthOnline'))
  else if (picgoNeeded.value) warning(t('toast.healthOffline'))
  else info(t('toast.healthNotNeeded'))
}

async function handleReveal(): Promise<void> {
  const target = revealTarget.value
  if (target === null) return
  try {
    await revealPath(target)
  } catch (error) {
    warning(t('error.revealOutput', { detail: describeError(error, t('error.unknown')) }))
  }
}

/** 行上的「在资源管理器里打开」：改完文档总要去看一眼 */
async function revealRow(path: string): Promise<void> {
  try {
    await revealPath(path)
  } catch (error) {
    warning(t('error.revealOutput', { detail: describeError(error, t('error.unknown')) }))
  }
}

async function handlePickDir(): Promise<void> {
  try {
    const paths = await pickFolder()
    const dir = paths[0]
    if (dir !== undefined) updateSettings({ outDir: dir })
  } catch (error) {
    if (isDialogUnsupported(error)) {
      warning(t('toast.dialogFallbackDir'))
      return
    }
    warning(t('error.pickDir', { detail: describeError(error, t('error.unknown')) }))
  }
}

/** 换视图时把键盘焦点跟着搬过去：否则 Esc 回到队列后，Tab 还是从页面开头走。 */
function goView(next: View): void {
  // 表单页改了一半就跑去开工：得说一声，不然下一次打开是那份改了一半的草稿，
  // 用户还以为是保存了。（输出 ↔ 设置之间来回切不算离开，两张单子共用一份草稿。）
  if (isFormView.value && jobModeOf(next) !== null && dirty.value) {
    warning(t('settings.unsavedLeft'))
  }
  const nextMode = jobModeOf(next)
  if (nextMode !== null) mode.value = nextMode
  view.value = next
  // 切菜单纯手点的话不该再画一圈焦点环：焦点照样给它（键盘能接着走），
  // 但明确告诉浏览器「这不是键盘操作」。用 Tab 走到这里时环还在。
  void nextTick(() => {
    const target = document.querySelector<HTMLElement>(`.rail__btn[data-nav="${next}"]`)
    target?.focus({ preventScroll: true, focusVisible: false } as FocusOptions)
  })
}

/** Ctrl/Cmd + Enter 开始转换；Esc 收起点开的东西。弹窗优先，关于在设置之上。 */
function handleKeydown(event: KeyboardEvent): void {
  const mod = event.ctrlKey || event.metaKey
  if (mod && event.key.toLowerCase() === 'o') {
    event.preventDefault()
    void pick(event.shiftKey ? 'folder' : 'files')
    return
  }
  if (mod && event.key === ',') {
    event.preventDefault()
    goView('settings')
    return
  }
  // 「/」在列表里是搜索：像浏览器那种习惯，按下去直接开始打字
  if (
    event.key === '/' &&
    !mod &&
    isJobView.value &&
    !(event.target as HTMLElement | null)?.closest('input, textarea')
  ) {
    event.preventDefault()
    queueRef.value?.openSearch()
    return
  }
  if (event.key === 'Escape') {
    if (closePrompt.value) {
      closePrompt.value = false
      return
    }
    if (aboutOpen.value) {
      aboutOpen.value = false
      return
    }
    if (isFormView.value) {
      goView(viewOfMode(mode.value))
      return
    }
  }
  if ((event.ctrlKey || event.metaKey) && event.key === 'Enter') {
    // 表单页上回车是「保存」——底栏那个「开始」按钮不在这两页上
    if (isFormView.value) {
      if (dirty.value) {
        event.preventDefault()
        void handleSaveSettings()
      }
      return
    }
    if (aboutOpen.value) return
    if (canStart.value) {
      event.preventDefault()
      handleRun()
    }
  }
}

onMounted(() => {
  // 后端点叉号时调用；浏览器预览里没有这个函数，弹窗也就不会出现。
  closeBridge.__markpicCloseRequest = handleCloseRequest
  connect()
  // 队列记在本地；加进来/清掉时同步写一次
  watch(
    () => [view.value, ...rowViews.value.map((row) => row.path)],
    () => writeQueues(),
  )
  window.setTimeout(() => void restoreQueue(), 700)
  void refreshHealth()
  void loadSettings()
  window.addEventListener('keydown', handleKeydown)
  // 启动后悄悄查一次（可在「关于」里关掉），查到就在侧边栏版本号旁点个记号。
  if (autoCheck.value) {
    window.setTimeout(() => void checkUpdate(true), 1200)
  }
})

onBeforeUnmount(() => {
  if (closeBridge.__markpicCloseRequest === handleCloseRequest) {
    delete closeBridge.__markpicCloseRequest
  }
  window.removeEventListener('keydown', handleKeydown)
  stopPolling()
  disconnect()
})
</script>

<template>
  <div class="app">
    <AppHeader
      :picgo="picgo"
      :debug="debug"
      :picgo-needed="picgoNeeded"
      :health-checking="healthLoading"
      :theme="theme"
      :phase="phase"
      :completed="completedCount"
      :total="totalCount"
      @refresh-health="handleRefreshHealth"
      @toggle-theme="toggleTheme"
    />

    <div class="app__body">
      <AppRail
        :view="view"
        :running="runningViews"
        :dirty="dirty"
        :version="version"
        :has-update="hasUpdate"
        @navigate="goView($event)"
        @about="aboutOpen = true"
      />

      <main class="app__view">
        <!-- 一张卡片 + 一条脚注：作业页的底栏不是卡片外面的一根横条，
             而是这张卡片的脚注 —— 否则切到设置页（脚注在卡片里），
             卡片下缘会上下跳 44px。 -->
        <div class="stage">
        <!-- 三个作业页各有各的版式：转换是文件表，检测是地址表，下载是「远程 → 本地」映射。
             页面的状态各自保留（useRun 里的 QueuePage），切走再回来还是原样。 -->
        <!-- 整页（内容 + 脚注）作为一层做交叉淡化：页与脚注一起淡，卡片下缘
             因此不会在过渡中途换高度；过渡只改透明度，不做位移。 -->
          <Transition name="pane">
          <div :key="view" class="pane">
          <FileQueue
            v-if="view === 'convert'"
            key="convert"
            ref="queueRef"
            :rows="rowViews"
            :phase="phase"
            :completed="completedCount"
            :total="totalCount"
            :remote-mode="mode"
            :busy="jobBusy"
            :progress="jobProgress"
            :aborted="stats.abortReason !== ''"
            :selected="selectedId"
            :recent="recentPaths"
            @pick-files="pick('files')"
            @pick-folder="pick('folder')"
            @pick-recent="enqueue([$event])"
            @remove="removeRow"
            @reveal="revealRow"
            @select="selectRow"
            @clear="clearQueue"
            @cancel-scan="handleCancelScan"
            @retry-scan="handleRetryScan"
          />
          <CheckPage
            v-else-if="view === 'check'"
            key="check"
            :rows="rowViews"
            :busy="jobBusy"
            :progress="jobProgress"
            :recent="recentPaths"
            @pick-files="pick('files')"
            @pick-folder="pick('folder')"
            @pick-recent="enqueue([$event])"
            @remove="removeRow"
            @reveal="revealRow"
            @clear="clearQueue"
            @cancel-scan="handleCancelScan"
            @retry-scan="handleRetryScan"
          />
          <DownloadPage
            v-else-if="view === 'download'"
            key="download"
            :rows="rowViews"
            :busy="jobBusy"
            :progress="jobProgress"
            :recent="recentPaths"
            @pick-files="pick('files')"
            @pick-folder="pick('folder')"
            @pick-recent="enqueue([$event])"
            @remove="removeRow"
            @reveal="revealRow"
            @clear="clearQueue"
            @cancel-scan="handleCancelScan"
            @retry-scan="handleRetryScan"
          />
          <OptionsPanel
            v-else
            key="settings"
            :settings="settings"
            :debug="debug"
            :picgo-needed="picgoNeeded"
            :dirty="dirty"
            :loaded="settingsLoaded"
            :loading="settingsLoading"
            :saving="settingsSaving"
            :load-error="settingsError"
            @update="updateSettings"
            @save="handleSaveSettings"
            @discard="discardSettings"
            @retry="loadSettings"
            @pick-dir="handlePickDir"
          />

        <AppFooter
          v-if="isJobView"
          :busy-elsewhere="runningElsewhere"
          :blocked-hint="startHint"
          :stats="stats"
          :phase="phase"
          :remote-mode="mode"
          :busy="jobBusy"
          :can-start="canStart"
          :can-reveal="canReveal"
          :debug="mode !== 'check' && debug"
          @run="handleRun"
          @cancel="cancel"
          @reveal="handleReveal"
        />
          </div>
          </Transition>
        </div>
      </main>
    </div>
    <!-- 拖文件进来时铺一层：不拦默认行为的话，WebView2 会直接导航到那个文件 -->
    <div v-if="dropActive" class="dropzone" aria-hidden="true">
      <span class="dropzone__pill">
        <AppIcon name="folder-plus" :size="16" />
        {{ t('drop.hint') }}
      </span>
    </div>

    <ToastStack :toasts="toasts" @dismiss="dismiss" />

    <!-- 关于 / 更新：版本号在侧边栏左下角，点那里打开 -->
    <AboutDialog
      v-if="aboutOpen"
      :version="version"
      :data-dir="dataDir"
      :data-note="dataNote"
      :settings-note="settingsNote"
      @close="aboutOpen = false"
    />

    <!-- 点叉号需要确认时：没有任务可选最小化或退出并勾「不再询问」，有任务只确认是否退出 -->
    <CloseConfirmDialog
      v-if="closePrompt"
      :count="closePromptCount"
      :ask="closePromptAsk"
      @cancel="dismissClosePrompt"
      @minimize="minimizeClosePrompt"
      @quit="quitClosePrompt"
    />

    <!-- 桌面窗口里四条边的缩放热区；浏览器里不需要 -->
    <div v-if="windowChrome" class="rz" aria-hidden="true">
      <span class="rz__zone rz__zone--n" @mousedown.prevent="startResize('top')" />
      <span class="rz__zone rz__zone--s" @mousedown.prevent="startResize('bottom')" />
      <span class="rz__zone rz__zone--w" @mousedown.prevent="startResize('left')" />
      <span class="rz__zone rz__zone--e" @mousedown.prevent="startResize('right')" />
      <span class="rz__zone rz__zone--nw" @mousedown.prevent="startResize('top-left')" />
      <span class="rz__zone rz__zone--ne" @mousedown.prevent="startResize('top-right')" />
      <span class="rz__zone rz__zone--sw" @mousedown.prevent="startResize('bottom-left')" />
      <span class="rz__zone rz__zone--se" @mousedown.prevent="startResize('bottom-right')" />
    </div>
  </div>
</template>

<style scoped>
.app {
  position: relative;
  display: grid;
  /* 底栏是一块浮起来的 dock（有自己的内边距与圆角），高度交给内容决定；
     写死 64px 会让它贴死在窗口底部，像一条状态栏。 */
  grid-template-rows: 40px minmax(0, 1fr);
  height: 100vh;
  /* 桌面窗口的最小宽度由后端 WM_GETMINMAXINFO 兜着（560 逻辑像素：500 的贴靠门槛与
     「正文别被导航挤扁」之间的折中，见 perf-log）；这里再留一道底线——480 实测仍
     不横向溢出，再窄就出现横向滚动条兜住版面，而不是把版面压垮。 */
  min-width: 480px;
  padding: 0 var(--gutter);
  color: var(--ink);
}

/* 三栏外壳：左边一条导航（后面加功能就往里塞），中间是当前视图，右边是本轮/选中的上下文。
   这一层只负责「放得下」，具体排版交给各视图自己。 */
.app__body {
  display: grid;
  grid-template-columns: 168px minmax(0, 1fr);
  gap: 12px;
  min-height: 0;
}

.app__view {
  position: relative;
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}

/* 卡片和它的脚注摞在一起，量高度时当一块算 */
.stage {
  position: relative;
  display: flex;
  flex: 1 1 auto;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}

/* 一页 = 内容 + 脚注。两层叠在同一个尺寸的盒子里交叉淡化，
   量到的下缘始终是 795，不会像以前那样在过渡中途换高度。 */
.pane {
  display: flex;
  flex: 1 1 auto;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}

/* 只有旧页在手背上淡出：新页一上来就是实心的，所以过渡中途不会
   透出页面底色（两边都半透明时亮度会掉一截，看着发灰）。 */
.pane-leave-active {
  position: absolute;
  z-index: 1;
  inset: 0;
  transition: opacity 140ms var(--ease);
}

.pane-leave-to {
  opacity: 0;
}

.app__view > *:first-child {
  flex: 1;
  min-height: 0;
}

/* 底栏是状态条，不参与拉伸 */
.app__view > :not(:first-child) {
  flex: none;
}

@media (max-width: 900px) {
  .app {
    grid-template-rows: 40px minmax(0, 1fr);
  }

  .app__body {
    /* 168px 在窄窗口里占掉窗口三分之一（560 宽时正文只剩 382px）。收到 148px：
       图标 + 两个字仍然放得下，正文多出 20px。 */
    grid-template-columns: 148px minmax(0, 1fr);
    gap: 10px;
    padding-bottom: 8px;
  }
}

/* 导航一直是「图标 + 文字」，没有收起成图标列的形态（见 README）；但宽窗口的 168px
   到了窄窗口就嫌宽，所以 900 以下收到 148px（在 .app__body 的 900px 断点里）。
   再窄的版面问题由页头换行与空态自身滚动解决，不靠收起导航。 */

/* 窗口缩放热区：平时不吃事件，只在这几条边上有反应 */
.rz {
  position: fixed;
  inset: 0;
  z-index: 40;
  pointer-events: none;
}

.rz__zone {
  position: absolute;
  pointer-events: auto;
}

.rz__zone--n,
.rz__zone--s {
  right: 10px;
  left: 10px;
  height: 5px;
}

.rz__zone--n {
  top: 0;
  cursor: ns-resize;
}

.rz__zone--s {
  bottom: 0;
  cursor: ns-resize;
}

.rz__zone--w,
.rz__zone--e {
  top: 10px;
  bottom: 10px;
  width: 5px;
}

.rz__zone--w {
  left: 0;
  cursor: ew-resize;
}

.rz__zone--e {
  right: 0;
  cursor: ew-resize;
}

.rz__zone--nw,
.rz__zone--ne,
.rz__zone--sw,
.rz__zone--se {
  width: 12px;
  height: 12px;
}

.rz__zone--nw {
  top: 0;
  left: 0;
  cursor: nwse-resize;
}

.rz__zone--ne {
  top: 0;
  right: 0;
  cursor: nesw-resize;
}

.rz__zone--sw {
  bottom: 0;
  left: 0;
  cursor: nesw-resize;
}

.rz__zone--se {
  right: 0;
  bottom: 0;
  cursor: nwse-resize;
}
/* 拖拽时铺满窗口的一层薄雾：只在这一瞬间出现，平时完全不占视觉 */
.dropzone {
  position: fixed;
  inset: 0;
  z-index: 45;
  display: grid;
  place-items: center;
  background: color-mix(in srgb, var(--canvas) 74%, transparent);
  backdrop-filter: blur(2px);
  pointer-events: none;
}

.dropzone__pill {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 12px 20px;
  border-radius: 999px;
  background: var(--panel);
  box-shadow: 0 18px 40px -22px rgba(13, 17, 23, 0.55);
  color: var(--ink);
  font-size: var(--fs-3);
}
</style>
