<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import AboutDialog from './components/AboutDialog.vue'
import AppFooter from './components/AppFooter.vue'
import AppHeader from './components/AppHeader.vue'
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
import { useRun } from './composables/useRun'
import { useSettings } from './composables/useSettings'
import { useTheme } from './composables/useTheme'
import { useToasts } from './composables/useToasts'
import { useUpdate } from './composables/useUpdate'
import { useWindowControls } from './composables/useWindowControls'
import { useI18n } from './i18n'

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

// 设置与关于都是弹窗，默认关着；需要时从标题栏 / 底栏打开
const optionsOpen = ref(false)
const aboutOpen = ref(false)
const manualOpen = ref(false)

const { hasUpdate, autoCheck, check: checkUpdate, stopPolling } = useUpdate()

const version = computed(() => health.value?.version ?? '')
const picgo = computed(() => health.value?.picgo ?? null)
// 服务端给的 needed 是按**已保存**的设置算的，而面板里的选择可能还没保存就开跑
// （整轮用的就是面板里这份）。这盏灯回答的是「现在能不能开始」，所以按面板里
// 真正会用的那份设置来判：只检测 / 只下载 / 试运行都不上传。
const picgoNeeded = computed(() => settings.remote === 'ignore' && !settings.dryRun)
const dataDir = computed(() => health.value?.dataDir ?? '')
const dataNote = computed(() => health.value?.dataNote ?? '')
const settingsNote = computed(() => health.value?.settingsNote ?? '')
const dryRun = computed(() => settings.dryRun)

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

async function enqueue(paths: string[]): Promise<void> {
  const added = addPaths(paths)
  if (added.length === 0) {
    if (!busy.value) warning(t('toast.pathsQueued'))
    return
  }
  manualOpen.value = false
  try {
    applyPreview(await preview(added, settingsSnapshot()), added)
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
    settingsSnapshot(),
  )
}

async function handleSaveSettings(): Promise<void> {
  if (await saveSettings()) await refreshHealth()
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

/** Ctrl/Cmd + Enter 开始转换；Esc 收起点开的东西。弹窗优先，关于在设置之上。 */
function handleKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    if (aboutOpen.value) {
      aboutOpen.value = false
      return
    }
    if (optionsOpen.value) {
      optionsOpen.value = false
      return
    }
    if (manualOpen.value) {
      manualOpen.value = false
      return
    }
  }
  if ((event.ctrlKey || event.metaKey) && event.key === 'Enter') {
    // 弹窗开着的时候，回车是「保存设置」——后面那个「开始转换」按钮被挡住了
    if (optionsOpen.value) {
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
      :options-open="optionsOpen"
      :phase="phase"
      :completed="completedCount"
      :total="totalCount"
      @refresh-health="refreshHealth"
      @toggle-theme="toggleTheme"
      @toggle-settings="optionsOpen = !optionsOpen"
    />

    <main class="app__main">
      <div class="workspace">
        <FileQueue
          :rows="rowViews"
          :phase="phase"
          :completed="completedCount"
          :total="totalCount"
          :manual-open="manualOpen"
          :remote-mode="settings.remote"
          @pick-files="pick('files')"
          @pick-folder="pick('folder')"
          @submit-paths="enqueue"
          @remove="removeRow"
          @reveal="revealRow"
          @clear="clearQueue"
          @update:manual-open="manualOpen = $event"
        />
      </div>
    </main>

    <!-- 设置是弹窗，独立于主栅格 -->
    <OptionsPanel
      v-if="optionsOpen"
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
      @close="optionsOpen = false"
      @pick-dir="handlePickDir"
    />

    <AppFooter
      :stats="stats"
      :phase="phase"
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
      :data-note="dataNote"`n      :settings-note="settingsNote"
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
  grid-template-rows: 44px minmax(0, 1fr) 64px;
  height: 100vh;
  /* 桌面窗口的最小宽度由后端 WM_GETMINMAXINFO 兜着（760 逻辑像素）；
     这里再留一道底线，浏览器里拖到更窄就出现横向滚动条，而不是把版面压垮。 */
  min-width: 620px;
  padding: 0 var(--gutter);
  color: var(--ink);
}

.app__main {
  min-height: 0;
  padding-bottom: 2px;
}

/* 一块面板浮在画布上；设置改成弹窗后，这里只剩队列 */
.workspace {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 14px;
  height: 100%;
}

@media (max-width: 900px) {
  .app {
    grid-template-rows: 44px minmax(0, 1fr) auto;
  }

  .app__main {
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
