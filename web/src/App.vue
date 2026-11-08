<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
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
import { useHealth } from './composables/useHealth'
import { useRun } from './composables/useRun'
import { useSettings } from './composables/useSettings'
import { useTheme } from './composables/useTheme'
import { useToasts } from './composables/useToasts'
import { useWindowControls } from './composables/useWindowControls'

const { toasts, dismiss, warning } = useToasts()
const { resolved: theme, toggle: toggleTheme } = useTheme()
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

// 设置现在是弹窗，默认关着；需要时从标题栏打开
const optionsOpen = ref(false)
const manualOpen = ref(false)

const version = computed(() => health.value?.version ?? null)
const picgo = computed(() => health.value?.picgo ?? null)
const dryRun = computed(() => settings.dryRun)

const canStart = computed(() => !busy.value && rowViews.value.length > 0)
const canReveal = computed(() => revealTarget.value !== null)

function dialogFallback(): void {
  warning('当前系统不支持原生对话框，请手动输入路径')
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
    warning(`选择路径失败：${describeError(error, '未知错误')}`)
  }
}

async function enqueue(paths: string[]): Promise<void> {
  const added = addPaths(paths)
  if (added.length === 0) {
    if (!busy.value) warning('这些路径已在队列中')
    return
  }
  manualOpen.value = false
  try {
    applyPreview(await preview(added, settingsSnapshot()))
  } catch (error) {
    warning(`读取图片数量失败：${describeError(error, '未知错误')}`)
  }
}

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
    warning(`打开输出目录失败：${describeError(error, '未知错误')}`)
  }
}

async function handlePickDir(): Promise<void> {
  try {
    const paths = await pickFolder()
    const dir = paths[0]
    if (dir !== undefined) updateSettings({ outDir: dir })
  } catch (error) {
    if (isDialogUnsupported(error)) {
      warning('当前系统不支持原生对话框，请手动输入输出目录')
      return
    }
    warning(`选择目录失败：${describeError(error, '未知错误')}`)
  }
}

/** Ctrl/Cmd + Enter 开始转换；Esc 收起点开的东西。设置是弹窗，优先处理它。 */
function handleKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
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
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', handleKeydown)
  disconnect()
})
</script>

<template>
  <div class="app">
    <AppHeader
      :version="version"
      :picgo="picgo"
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
          @pick-files="pick('files')"
          @pick-folder="pick('folder')"
          @submit-paths="enqueue"
          @remove="removeRow"
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
      @run="handleRun"
      @cancel="cancel"
      @reveal="handleReveal"
    />

    <ToastStack :toasts="toasts" @dismiss="dismiss" />

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
</style>
