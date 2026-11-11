<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import AboutDialog from './components/AboutDialog.vue'
import AppFooter from './components/AppFooter.vue'
import AppHeader from './components/AppHeader.vue'
import AppRail from './components/AppRail.vue'
import CommandPalette from './components/CommandPalette.vue'
import type { PaletteCommand } from './components/CommandPalette.vue'
import FileQueue from './components/FileQueue.vue'
import OptionsPanel from './components/OptionsPanel.vue'
import OutputPanel from './components/OutputPanel.vue'
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
import { useRun } from './composables/useRun'
import { useSettings } from './composables/useSettings'
import { useTheme } from './composables/useTheme'
import { useToasts } from './composables/useToasts'
import { useUpdate } from './composables/useUpdate'
import { useWindowControls } from './composables/useWindowControls'
import { useI18n } from './i18n'
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
const aboutOpen = ref(false)
const manualOpen = ref(false)
const paletteOpen = ref(false)
// 选中哪一行：只用来给那一行描边，明细由行自己展开。
const selectedId = ref<string | null>(null)

// 侧边栏有两种形态：窄条（只有图标）和展开（图标 + 文字）。
// 这是「这台机器上我习惯哪种」的偏好，和语言、主题一样存在浏览器里，
// 不写进 settings.json —— 换台机器想要另一种形态，不该被配置文件绑住。
const RAIL_KEY = 'markpic.rail'

function readRailMode(): 'compact' | 'expanded' {
  try {
    return localStorage.getItem(RAIL_KEY) === 'expanded' ? 'expanded' : 'compact'
  } catch {
    return 'compact'
  }
}

const railMode = ref<'compact' | 'expanded'>(readRailMode())

function toggleRail(): void {
  railMode.value = railMode.value === 'compact' ? 'expanded' : 'compact'
  try {
    localStorage.setItem(RAIL_KEY, railMode.value)
  } catch {
    /* 存不进去也没关系，这一轮里照样能切 */
  }
}

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

const canStart = computed(() => !busy.value && rowViews.value.length > 0)
const canReveal = computed(() => revealTarget.value !== null)

/** 路径只留最后一段：命令面板里一排长路径读不出谁是谁。 */
/** 命令面板里的最近路径：最后两段，同名目录才分得清；完整路径放在右列。 */
function shortName(path: string): string {
  const parts = path.replace(/[\\/]+$/, '').split(/[\\/]+/)
  return parts.length >= 2 ? parts.slice(-2).join('\\') : parts[0] ?? path
}

// 命令面板只列「现在真的能做」的事：跑着的时候不出现「开始」，没开始的时候不出现「取消」。
const paletteCommands = computed<PaletteCommand[]>(() => {
  const commands: PaletteCommand[] = []
  if (!busy.value) {
    commands.push({ id: 'pick-files', label: t('palette.cmd.pickFiles'), icon: 'file-plus', run: () => void pick('files') })
    commands.push({ id: 'pick-folder', label: t('palette.cmd.pickFolder'), icon: 'folder-plus', run: () => void pick('folder') })
    // 最近用过的路径：名字在前，整条路径当提示，回车就等于重新入队
    for (const path of recentPaths.value.slice(0, 3)) {
      commands.push({
        id: `recent-${path}`,
        label: shortName(path),
        hint: path,
        icon: 'folder',
        run: () => void enqueue([path]),
      })
    }
  }
  if (canStart.value) {
    commands.push({ id: 'start', label: t('palette.cmd.start'), icon: 'play', hint: 'Ctrl ↵', run: handleRun })
  }
  if (busy.value) {
    commands.push({ id: 'cancel', label: t('palette.cmd.cancel'), icon: 'x', run: cancel })
  }
  if (canReveal.value) {
    commands.push({ id: 'reveal', label: t('palette.cmd.reveal'), icon: 'folder-open', run: () => void handleReveal() })
  }
  if (!busy.value && rowViews.value.length > 0) {
    commands.push({ id: 'clear', label: t('palette.cmd.clear'), icon: 'trash', run: clearQueue })
  }
  // 菜单上的四项就是四个功能，命令面板里也该能直接跳过去：
  // 以前「检测远程图」只是设置里的一个单选，连个名字都没有。
  commands.push({ id: 'nav-convert', label: t('palette.cmd.convert'), icon: 'image', run: () => { goView('convert') } })
  commands.push({ id: 'nav-check', label: t('palette.cmd.check'), icon: 'link', run: () => { goView('check') } })
  commands.push({ id: 'nav-download', label: t('palette.cmd.download'), icon: 'download', run: () => { goView('download') } })
  commands.push({ id: 'nav-output', label: t('palette.cmd.output'), icon: 'folder-open', run: () => { goView('output') } })
  commands.push({ id: 'settings', label: t('palette.cmd.settings'), icon: 'settings', run: () => { goView('settings') } })
  commands.push({ id: 'theme', label: t('palette.cmd.theme'), icon: theme.value === 'dark' ? 'moon' : 'sun', run: toggleTheme })
  commands.push({ id: 'about', label: t('palette.cmd.about'), icon: 'info', run: () => { aboutOpen.value = true } })
  return commands
})

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
   只记路径，不记结果 —— 结果要重新扫一遍才算数。 */
const QUEUE_KEY = 'markpic.queue'

function readQueue(): string[] {
  try {
    const raw = JSON.parse(localStorage.getItem(QUEUE_KEY) ?? '[]')
    return Array.isArray(raw) ? raw.filter((item): item is string => typeof item === 'string') : []
  } catch {
    return []
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

/** 启动时把上次的队列接回来；正在跑的那一轮由 run.current 负责，别抢。 */
async function restoreQueue(): Promise<void> {
  if (rowViews.value.length > 0 || busy.value) return
  const saved = readQueue()
  if (saved.length === 0) return
  const added = addPaths(saved)
  if (added.length === 0) return
  try {
    applyPreview(await preview(added, jobSettings()), added)
  } catch {
    /* 预览失败就当没接回来，行还在，用户点开始也行 */
  }
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
    !paletteOpen.value &&
    !(event.target as HTMLElement | null)?.closest('input, textarea')
  ) {
    event.preventDefault()
    queueRef.value?.openSearch()
    return
  }
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k') {
    if (paletteOpen.value) return
    event.preventDefault()
    paletteOpen.value = true
    return
  }
  if (event.key === 'Escape') {
    if (paletteOpen.value) {
      paletteOpen.value = false
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
    () => rowViews.value.map((row) => row.path),
    (paths) => {
      try {
        if (paths.length === 0) localStorage.removeItem(QUEUE_KEY)
        else localStorage.setItem(QUEUE_KEY, JSON.stringify(paths))
      } catch {
        /* 存不下就算了 */
      }
    },
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
      @open-palette="paletteOpen = true"
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

    <div class="app__body" :class="{ 'app__body--wide': railMode === 'expanded' }">
      <AppRail
        :view="view"
        :dirty="dirty"
        :mode="railMode"
        @navigate="goView($event)"
        @about="aboutOpen = true"
        @toggle="toggleRail"
      />

      <main class="app__view">
        <FileQueue
          v-if="isJobView"
          ref="queueRef"
          :rows="rowViews"
          :phase="phase"
          :completed="completedCount"
          :total="totalCount"
          :manual-open="manualOpen"
          :remote-mode="mode"
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
        <OutputPanel
          v-else-if="view === 'output'"
          :settings="settings"
          :dirty="dirty"
          :loaded="settingsLoaded"
          :loading="settingsLoading"
          :saving="settingsSaving"
          :load-error="settingsError"
          @update="updateSettings"
          @save="handleSaveSettings"
          @discard="discardSettings"
          @retry="loadSettings"
          @close="goView(viewOfMode(mode))"
          @pick-dir="handlePickDir"
        />
        <OptionsPanel
          v-else
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
          @close="goView(viewOfMode(mode))"
        />

        <AppFooter
          v-if="isJobView"
          :stats="stats"
          :phase="phase"
          :remote-mode="mode"
          :busy="busy"
          :can-start="canStart"
          :can-reveal="canReveal"
          :dry-run="dryRun"
          :version="version"
          :has-update="hasUpdate"
          @run="handleRun"
          @cancel="cancel"
          @reveal="handleReveal"
          @about="aboutOpen = true"
        />
      </main>
    </div>
    <!-- 拖文件进来时铺一层：不拦默认行为的话，WebView2 会直接导航到那个文件 -->
    <div v-if="dropActive" class="dropzone" aria-hidden="true">
      <span class="dropzone__pill">
        <AppIcon name="folder-plus" :size="16" />
        {{ t('drop.hint') }}
      </span>
    </div>

    <CommandPalette v-if="paletteOpen" :commands="paletteCommands" @close="paletteOpen = false" />

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
  grid-template-columns: 56px minmax(0, 1fr);
  gap: 12px;
  min-height: 0;
}

/* 侧边栏展开时把导航文字放出来，内容区相应让位。 */
.app__body--wide {
  grid-template-columns: 196px minmax(0, 1fr);
}

.app__view {
  display: flex;
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
    grid-template-columns: 48px minmax(0, 1fr);
    gap: 10px;
    padding-bottom: 8px;
  }

  .app__body--wide {
    grid-template-columns: 176px minmax(0, 1fr);
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
