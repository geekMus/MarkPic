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

const { toasts, dismiss, warning } = useToasts()
const { resolved: theme, toggle: toggleTheme } = useTheme()
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

const optionsOpen = ref(true)
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

onMounted(() => {
  connect()
  void refreshHealth()
  void loadSettings()
})

onBeforeUnmount(() => {
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

    <main class="app__main" :class="{ 'app__main--aside': optionsOpen }">
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
    </main>

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
  </div>
</template>

<style scoped>
.app {
  display: grid;
  grid-template-rows: 56px minmax(0, 1fr) 56px;
  height: 100vh;
  background: var(--bg);
  color: var(--text-1);
}

.app__main {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 12px;
  min-height: 0;
  padding: 12px 14px;
  overflow: hidden;
}

.app__main--aside {
  grid-template-columns: minmax(0, 1fr) 320px;
}

@media (max-width: 900px) {
  .app__main--aside {
    grid-template-columns: minmax(0, 1fr);
    grid-template-rows: minmax(0, 1fr) minmax(220px, 42vh);
  }
}
</style>
