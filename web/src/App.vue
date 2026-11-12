<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import AboutDialog from './components/AboutDialog.vue'
import AppFooter from './components/AppFooter.vue'
import AppHeader from './components/AppHeader.vue'
import AppRail from './components/AppRail.vue'
import CheckPage from './components/CheckPage.vue'
import DownloadPage from './components/DownloadPage.vue'
import FileQueue from './components/FileQueue.vue'
import OptionsPanel from './components/OptionsPanel.vue'
import ToastStack from './components/ToastStack.vue'
import {
  describeError,
  isDialogUnsupported,
  pickFiles,
  pickFolder,
  preview,
  revealPath,
} from './api'
import { useFileDrop } from './composables/useFileDrop'
import { useHealth } from './composables/useHealth'
import {
  applyPreviewOn,
  currentQueuePage,
  queuePagePaths,
  queuePagePendingPreview,
  restoreQueuePage,
  runningQueuePage,
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

const { toasts, dismiss, warning } = useToasts()
const { t } = useI18n()
const { resolved: theme, cycle: toggleTheme } = useTheme()
// 窗口没有系统边框，缩放热区得由界面在四条边上铺出来
const { available: windowChrome, startResize } = useWindowControls()
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
  update: updateSettings,
  discard: discardSettings,
  snapshot: settingsSnapshot,
} = useSettings()

const {
  phase,
  rowViews,
  stats,
  busy,
  totalCount,
  completedCount,
  imageTotal,
  imageDone,
  revealTarget,
  connect,
  disconnect,
  addPaths,
  applyPreview,
  start,
  cancel,
  removeRow,
  clearQueue,
} = useRun()

// 主视图 = 侧边栏上的菜单项。左边三项是作业视图（共用一条队列，区别只在远程图
// 怎么处理），右边两项是表单页（结果写到哪 / 怎么跑）。设置不再占用弹窗。
const view = ref<View>('convert')

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
const JOB_MODES: readonly RemoteMode[] = ['ignore', 'check', 'download']

watch(
  view,
  (next) => {
    const nextMode = jobModeOf(next)
    if (nextMode === null) return
    setQueuePage(nextMode)
    // 切到哪一页，就补扫那一页还没统计过的行（启动时只扫了当时停留的那一页）
    void previewPage(nextMode)
  },
  { immediate: true },
)

/** 哪个作业页正在跑：侧边栏那个按钮上点个记号，别让人以为作业丢了。 */
const runningView = computed<View | null>(() => {
  const owner = runningQueuePage()
  return owner === null ? null : viewOfMode(owner)
})

/** 当前这一页是不是正在跑：页头下面那条细线只在跑的时候出现。 */
const jobBusy = computed(() => busy.value && runningView.value === view.value)

/** 这一轮的进度（0..1），给页头那条细线用。 */
const jobProgress = computed(() => {
  const files = totalCount.value > 0 ? completedCount.value / totalCount.value : 0
  // 一轮只有一个文件时文件级进度永远停在 0，取图张数进度里更靠前的那个
  const images = imageTotal.value > 0 ? imageDone.value / imageTotal.value : 0
  return Math.max(files, Math.min(1, images))
})

/** 跑着的画面不在眼前时，底栏说一句「转换 正在运行」，否则开始按钮灰着没人知道为什么。 */
const runningElsewhere = computed<string>(() => {
  const owner = runningQueuePage()
  if (owner === null || owner === mode.value) return ''
  return t('footer.runningElsewhere', { page: t(`nav.${viewOfMode(owner)}` as MessageKey) })
})
const aboutOpen = ref(false)
const manualOpen = ref(false)
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
// 真正会用的那份设置来判：只检测 / 只下载 / 试运行都不上传。
const picgoNeeded = computed(() => mode.value === 'ignore' && !settings.dryRun)
const dataDir = computed(() => health.value?.dataDir ?? '')
const dataNote = computed(() => health.value?.dataNote ?? '')
const settingsNote = computed(() => health.value?.settingsNote ?? '')
const dryRun = computed(() => settings.dryRun)

// 队列面板标题右边那枚小标签：这一刻这件事的结果落在哪儿。
// 三个作业视图共用一份队列，只有这一行和底栏按钮能说明「我切到哪了」。
const jobTag = computed(() => {
  if (mode.value === 'check') return t('view.tag.readonly')
  if (mode.value === 'download') {
    const dir = settings.remoteDir.trim() === '' ? 'images' : settings.remoteDir.trim()
    return t('view.tag.into', { dir })
  }
  if (settings.dryRun) return t('view.tag.dry')
  const endpoint = settings.endpoint.trim()
  if (endpoint === '') return t('view.tag.noPicgo')
  return endpoint.replace(/^https?:\/\//, '').replace(/\/+$/, '')
})

const canStart = computed(() => !busy.value && rowViews.value.length > 0)
const canReveal = computed(() => revealTarget.value !== null)

function dialogFallback(): void {
  warning(t('toast.dialogFallback'))
  manualOpen.value = true
}

async function pick(kind: 'files' | 'folder'): Promise<void> {
  try {
    const paths = kind === 'files' ? await pickFiles() : await pickFolder()
    if (paths.length === 0) return
    await enqueue(paths)
  } catch (error) {
    if (isDialogUnsupported(error)) {
      dialogFallback()
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

/** 扫一遍某一页里还没统计过的行（切到这一页时顺手做，别让三页在启动时抢网络）。 */
async function previewPage(pageMode: RemoteMode): Promise<void> {
  const paths = queuePagePendingPreview(pageMode)
  if (paths.length === 0) return
  try {
    applyPreviewOn(pageMode, await preview(paths, settingsSnapshot({ remote: pageMode })), paths)
  } catch {
    /* 预览失败就当没扫过，行还在，用户点开始也行 */
  }
}

/** 启动时把三页各自的队列接回来；正在跑的那一轮由 run.current 负责，别抢。 */
async function restoreQueue(): Promise<void> {
  if (busy.value) return
  const saved = readQueues()
  for (const pageMode of JOB_MODES) {
    const paths = saved[pageMode] ?? []
    if (paths.length === 0) continue
    restoreQueuePage(pageMode, paths)
  }
  void previewPage(currentQueuePage())
}

async function enqueue(paths: string[]): Promise<void> {
  const added = addPaths(paths)
  if (added.length === 0) {
    if (!busy.value) warning(t('toast.pathsQueued'))
    return
  }
  manualOpen.value = false
  rememberPaths(added)
  try {
    applyPreview(await preview(added, jobSettings()), added)
  } catch (error) {
    warning(t('error.preview', { detail: describeError(error, t('error.unknown')) }))
  }
}
// 拖进窗口就等于加进队列；拦不住 drag 事件的话 WebView2 会把窗口导航到那个文件
const { active: dropActive } = useFileDrop({
  onPaths: (paths) => void enqueue(paths),
  onUnsupported: () => warning(t('drop.unsupported')),
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
  void nextTick(() => document.querySelector<HTMLElement>(`.rail__btn[data-nav="${next}"]`)?.focus())
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
    if (aboutOpen.value) {
      aboutOpen.value = false
      return
    }
    if (isFormView.value) {
      goView(viewOfMode(mode.value))
      return
    }
    if (manualOpen.value) {
      manualOpen.value = false
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
  // 启动后悄悄查一次（可在「关于」里关掉），查到就在底栏版本号旁点个记号。
  if (autoCheck.value) {
    window.setTimeout(() => void checkUpdate(true), 1200)
  }
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', handleKeydown)
  stopPolling()
  disconnect()
})
</script>

<template>
  <div class="app">
    <AppHeader
      :picgo="picgo"
      :picgo-needed="picgoNeeded"
      :health-checking="healthLoading"
      :theme="theme"
      :phase="phase"
      :completed="completedCount"
      :total="totalCount"
      @refresh-health="refreshHealth"
      @toggle-theme="toggleTheme"
    />

    <div class="app__body">
      <AppRail
        :view="view"
        :running="runningView"
        :dirty="dirty"
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
        <!-- 切页就是换页：旧页立刻下、新页立刻在位，只让页内文字淡进来。
             试过位移和叠层交叉淡化，都会让人看到「跳」，所以这里不挂 Transition。 -->
          <FileQueue
            v-if="view === 'convert'"
            key="convert"
            ref="queueRef"
            :rows="rowViews"
            :phase="phase"
            :completed="completedCount"
            :total="totalCount"
            :manual-open="manualOpen"
            :remote-mode="mode"
            :mode-tag="jobTag"
            :busy="jobBusy"
            :progress="jobProgress"
            :aborted="stats.abortReason !== ''"
            :selected="selectedId"
            :recent="recentPaths"
            @pick-files="pick('files')"
            @pick-folder="pick('folder')"
            @pick-recent="enqueue([$event])"
            @submit-paths="enqueue"
            @remove="removeRow"
            @reveal="revealRow"
            @select="selectRow"
            @clear="clearQueue"
            @update:manual-open="manualOpen = $event"
          />
          <CheckPage
            v-else-if="view === 'check'"
            key="check"
            :rows="rowViews"
            :tag="jobTag"
            :busy="jobBusy"
            :progress="jobProgress"
            :manual-open="manualOpen"
            :recent="recentPaths"
            @pick-files="pick('files')"
            @pick-folder="pick('folder')"
            @pick-recent="enqueue([$event])"
            @submit-paths="enqueue"
            @clear="clearQueue"
            @update:manual-open="manualOpen = $event"
          />
          <DownloadPage
            v-else-if="view === 'download'"
            key="download"
            :rows="rowViews"
            :tag="jobTag"
            :busy="jobBusy"
            :progress="jobProgress"
            :manual-open="manualOpen"
            :recent="recentPaths"
            @pick-files="pick('files')"
            @pick-folder="pick('folder')"
            @pick-recent="enqueue([$event])"
            @submit-paths="enqueue"
            @remove="removeRow"
            @clear="clearQueue"
            @update:manual-open="manualOpen = $event"
          />
          <OptionsPanel
            v-else
            key="settings"
            :settings="settings"
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
          :stats="stats"
          :phase="phase"
          :remote-mode="mode"
          :busy="busy"
          :can-start="canStart"
          :can-reveal="canReveal"
          :dry-run="mode !== 'check' && dryRun"
          :version="version"
          :has-update="hasUpdate"
          @run="handleRun"
          @cancel="cancel"
          @reveal="handleReveal"
          @about="aboutOpen = true"
        />
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

    <!-- 关于 / 更新：版本号挪到页面底部后，从那里点开 -->
    <AboutDialog
      v-if="aboutOpen"
      :version="version"
      :data-dir="dataDir"
      :data-note="dataNote"
      :settings-note="settingsNote"
      @close="aboutOpen = false"
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
  /* 桌面窗口的最小宽度由后端 WM_GETMINMAXINFO 兜着（760 逻辑像素）；
     这里再留一道底线，浏览器里拖到更窄就出现横向滚动条，而不是把版面压垮。 */
  min-width: 620px;
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
  display: flex;
  flex: 1 1 auto;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
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
    gap: 10px;
    padding-bottom: 8px;
  }
}

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
