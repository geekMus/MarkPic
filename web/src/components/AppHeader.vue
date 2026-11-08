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
  return `运行中 ${props.completed}/${props.total}`
})

const themeTitle = computed(() => (props.theme === 'dark' ? '切换到浅色主题' : '切换到深色主题'))
</script>

<template>
  <header class="bar">
    <div class="bar__brand">
      <span class="bar__mark" aria-hidden="true">
        <svg viewBox="0 0 24 24" fill="none">
          <rect x="1.5" y="1.5" width="21" height="21" rx="6.5" fill="var(--mark)" />
          <path
            d="M8.6 8.4h4.2a1.6 1.6 0 0 1 1.6 1.6v4.2"
            stroke="var(--mark-ink)"
            stroke-width="1.6"
            stroke-linecap="round"
            fill="none"
          />
          <path
            d="M15.4 8.6v6.8H8.6"
            stroke="var(--mark-ink)"
            stroke-width="1.6"
            stroke-linecap="round"
            fill="none"
            opacity="0.55"
          />
        </svg>
      </span>

      <span class="bar__names">
        <span class="bar__title">Markdown 图片图床化</span>
        <span class="bar__sub">本地图片经 PicGo 上传，回写为图床链接</span>
      </span>

      <span v-if="versionLabel" class="bar__version mono">{{ versionLabel }}</span>
    </div>

    <div class="bar__tools">
      <StatusPill v-if="runVisible" :label="runLabel" tone="accent" pulse />

      <button
        type="button"
        class="health"
        :data-tone="picgoTone"
        :title="picgoTitle"
        @click="emit('refresh-health')"
      >
        <span class="health__dot" aria-hidden="true" />
        <span class="health__text">{{ picgoLabel }}</span>
      </button>

      <span class="bar__sep" aria-hidden="true" />

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
        :class="{ 'icon-btn--active': optionsOpen }"
        :title="optionsOpen ? '隐藏设置面板' : '显示设置面板'"
        :aria-label="optionsOpen ? '隐藏设置面板' : '显示设置面板'"
        :aria-pressed="optionsOpen"
        @click="emit('toggle-settings')"
      >
        <AppIcon name="settings" :size="16" />
      </button>
    </div>
  </header>
</template>

<style scoped>
.bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  min-width: 0;
}

.bar__brand {
  display: flex;
  align-items: center;
  gap: 11px;
  min-width: 0;
}

.bar__mark {
  display: grid;
  place-items: center;
  flex: none;
  width: 32px;
  height: 32px;
  border-radius: 10px;
  box-shadow: 0 1px 2px rgba(13, 17, 23, 0.18), 0 8px 18px -12px rgba(13, 17, 23, 0.7);
}

.bar__mark svg {
  width: 32px;
  height: 32px;
}

.bar__names {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
}

.bar__title {
  overflow: hidden;
  color: var(--ink);
  font-size: var(--fs-4);
  font-weight: 600;
  letter-spacing: -0.005em;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.bar__sub {
  overflow: hidden;
  color: var(--ink-3);
  font-size: var(--fs-1);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.bar__version {
  flex: none;
  padding: 1px 7px;
  border: 1px solid var(--line-2);
  border-radius: var(--r-pill);
  background: var(--panel);
  color: var(--ink-3);
  font-size: var(--fs-1);
  line-height: 17px;
}

.bar__tools {
  display: flex;
  align-items: center;
  gap: 6px;
  flex: none;
}

.bar__sep {
  width: 1px;
  height: 20px;
  margin: 0 4px;
  background: var(--line-2);
}

/* PicGo 状态读成一个可点的徽标，悬停才浮起来 */
.health {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  height: 30px;
  padding: 0 11px;
  border: 1px solid var(--line-2);
  border-radius: var(--r-pill);
  background: var(--panel);
  color: var(--ink-2);
  font-size: var(--fs-2);
  cursor: pointer;
  transition:
    border-color var(--t-fast) var(--ease),
    background var(--t-fast) var(--ease),
    box-shadow var(--t-fast) var(--ease);
}

.health:hover {
  border-color: var(--line-3);
  background: var(--panel-2);
  box-shadow: 0 1px 2px rgba(13, 17, 23, 0.06);
}

.health__dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--ink-3);
}

.health[data-tone='success'] .health__dot {
  background: var(--ok);
  box-shadow: 0 0 0 3px var(--ok-soft);
}

.health[data-tone='danger'] .health__dot {
  background: var(--bad);
  box-shadow: 0 0 0 3px var(--bad-soft);
}

.health[data-tone='neutral'] .health__dot {
  background: var(--warn);
  box-shadow: 0 0 0 3px var(--warn-soft);
}

.health[data-tone='success'] .health__text {
  color: var(--ok);
}

.health[data-tone='danger'] .health__text {
  color: var(--bad);
}

@media (max-width: 940px) {
  .bar__sub {
    display: none;
  }
}

@media (max-width: 720px) {
  .bar__names {
    display: none;
  }

  .bar__version {
    display: none;
  }
}
</style>
