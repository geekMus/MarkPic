<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import StatusPill from './StatusPill.vue'
import type { Health } from '../types'
import type { RunPhase } from '../composables/useRun'

const props = defineProps<{
  version: string | null
  picgo: Health['picgo'] | null
  healthChecking: boolean
  theme: 'light' | 'dark'
  optionsOpen: boolean
  phase: RunPhase
  completed: number
  total: number
}>()

const emit = defineEmits<{
  'refresh-health': []
  'toggle-theme': []
  'toggle-settings': []
}>()

const versionLabel = computed(() => {
  const value = props.version
  if (!value) return ''
  return value.startsWith('v') ? value : `v${value}`
})

const picgoTone = computed<'neutral' | 'success' | 'danger'>(() => {
  if (props.picgo === null) return props.healthChecking ? 'neutral' : 'danger'
  return props.picgo.online ? 'success' : 'danger'
})

const picgoLabel = computed(() => {
  if (props.picgo === null) return props.healthChecking ? '检测中' : '服务未连接'
  return props.picgo.online ? 'PicGo 已连接' : 'PicGo 未连接'
})

const picgoTitle = computed(() => {
  const detail = props.picgo?.detail.trim() ?? ''
  return detail === '' ? '点击重新检测 PicGo 连接' : `${detail}，点击重新检测`
})

const runVisible = computed(
  () => props.phase === 'preparing' || props.phase === 'running' || props.phase === 'cancelling',
)

const runLabel = computed(() => {
  if (props.phase === 'preparing') return '正在启动'
  if (props.phase === 'cancelling') return '正在取消'
  return `运行中 · ${props.completed}/${props.total}`
})

const themeTitle = computed(() => (props.theme === 'dark' ? '切换到浅色主题' : '切换到深色主题'))
</script>

<template>
  <header class="header">
    <div class="header__brand">
      <svg class="header__logo" viewBox="0 0 24 24" aria-hidden="true">
        <rect x="1.5" y="1.5" width="21" height="21" rx="6.5" fill="var(--accent)" />
        <path
          d="M8.6 8.4h4.2a1.6 1.6 0 0 1 1.6 1.6v4.2"
          stroke="var(--accent-fg)"
          stroke-width="1.6"
          stroke-linecap="round"
          fill="none"
        />
        <path
          d="M15.4 8.6v6.8H8.6"
          stroke="var(--accent-fg)"
          stroke-width="1.6"
          stroke-linecap="round"
          stroke-linejoin="round"
          fill="none"
        />
        <path
          d="M10.6 13.4l4.8-4.8"
          stroke="var(--accent-fg)"
          stroke-width="1.6"
          stroke-linecap="round"
          fill="none"
        />
      </svg>

      <h1 class="header__title">Markdown 图片图床化</h1>
      <span v-if="versionLabel" class="tag header__version" :title="`本地服务版本 ${versionLabel}`">
        {{ versionLabel }}
      </span>
    </div>

    <div class="header__actions">
      <span v-if="runVisible" class="header__run" role="status" aria-live="polite">
        <StatusPill tone="accent" :label="runLabel" pulse />
      </span>

      <button
        type="button"
        class="header__health"
        :title="picgoTitle"
        :aria-label="picgoTitle"
        @click="emit('refresh-health')"
      >
        <StatusPill :tone="picgoTone" :label="picgoLabel" :pulse="healthChecking" />
      </button>

      <button
        type="button"
        class="icon-btn"
        :title="themeTitle"
        :aria-label="themeTitle"
        @click="emit('toggle-theme')"
      >
        <AppIcon :name="theme === 'dark' ? 'sun' : 'moon'" :size="16" />
      </button>

      <button
        type="button"
        class="icon-btn"
        title="显示或隐藏选项面板"
        aria-label="显示或隐藏选项面板"
        :aria-pressed="optionsOpen"
        @click="emit('toggle-settings')"
      >
        <AppIcon name="settings" :size="16" />
      </button>
    </div>
  </header>
</template>

<style scoped>
.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  height: 56px;
  padding: 0 14px;
  border-bottom: 1px solid var(--border);
  background: var(--surface);
  z-index: 20;
}

.header__brand {
  display: flex;
  align-items: center;
  gap: 9px;
  min-width: 0;
}

.header__logo {
  width: 22px;
  height: 22px;
  flex: none;
}

.header__title {
  color: var(--text-1);
  font-size: 13.5px;
  font-weight: 600;
  letter-spacing: 0.01em;
  white-space: nowrap;
}

.header__version {
  font-variant-numeric: tabular-nums;
}

.header__actions {
  display: flex;
  align-items: center;
  gap: 6px;
  flex: none;
}

.header__run {
  display: inline-flex;
  margin-right: 2px;
}

.header__health {
  display: inline-flex;
  padding: 0;
  border: none;
  border-radius: var(--r-pill);
  background: transparent;
  cursor: pointer;
  transition: opacity var(--dur-hover) var(--ease);
}

.header__health:hover {
  opacity: 0.82;
}

@media (max-width: 780px) {
  .header__version {
    display: none;
  }

  .header__health :deep(.pill__label) {
    display: none;
  }

  .header__health :deep(.pill) {
    padding: 0 8px;
  }
}
</style>
