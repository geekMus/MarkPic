<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import StatusPill from './StatusPill.vue'
import { useWindowControls } from '../composables/useWindowControls'
import type { Health } from '../types'
import type { RunPhase } from '../composables/useRun'

const props = defineProps<{
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

const { available: windowChrome, maximized, minimize, toggleMaximize, close, startDrag } = useWindowControls()

// 字标只写名字，中文说明挂在悬停提示里
const brandTitle = 'convert-md-picture · Markdown 图片图床化'

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

// 双击标题栏空白处 = 最大化 / 还原，和系统标题栏一致。
function handleDoubleClick(event: MouseEvent): void {
  if (!windowChrome.value) return
  const target = event.target as HTMLElement | null
  if (target?.closest('[data-no-drag]')) return
  toggleMaximize()
}
</script>

<template>
  <header class="tb" @mousedown="startDrag" @dblclick="handleDoubleClick">
    <div class="tb__brand">
      <span class="tb__mark" aria-hidden="true">
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
      <!-- 品牌就是命令行里的那个名字。md 是它在做的事（改写 Markdown 图片链接），
           所以把这一段点亮，其余保持普通字色：看着像命令，也像被高亮的 token。 -->
      <span class="brand" role="img" aria-label="convert-md-picture" :title="brandTitle">
        <span class="brand__word" aria-hidden="true">convert</span>
        <span class="brand__dash" aria-hidden="true">-</span>
        <span class="brand__word brand__word--md" aria-hidden="true">md</span>
        <span class="brand__dash" aria-hidden="true">-</span>
        <span class="brand__word" aria-hidden="true">picture</span>
      </span>
    </div>

    <div class="tb__status">
      <StatusPill v-if="runVisible" :label="runLabel" tone="accent" pulse />
    </div>

    <div class="tb__tools" data-no-drag>
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

      <span class="tb__sep" aria-hidden="true" />

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
        :title="optionsOpen ? '关闭设置' : '打开设置'"
        :aria-label="optionsOpen ? '关闭设置' : '打开设置'"
        :aria-pressed="optionsOpen"
        @click="emit('toggle-settings')"
      >
        <AppIcon name="settings" :size="16" />
      </button>
    </div>

    <!-- 窗口按钮只在桌面窗口里出现；浏览器里没有系统标题栏可替代，直接隐藏 -->
    <div v-if="windowChrome" class="tb__win" data-no-drag>
      <button type="button" class="wbtn" title="最小化" aria-label="最小化" @click="minimize">
        <svg viewBox="0 0 10 10" aria-hidden="true">
          <path d="M0.5 5.5h9" />
        </svg>
      </button>
      <button
        type="button"
        class="wbtn"
        :title="maximized ? '向下还原' : '最大化'"
        :aria-label="maximized ? '向下还原' : '最大化'"
        @click="toggleMaximize"
      >
        <svg v-if="maximized" viewBox="0 0 10 10" aria-hidden="true">
          <path d="M2.5 2.5V0.5h7v7h-2" />
          <rect x="0.5" y="2.5" width="7" height="7" rx="1.5" />
        </svg>
        <svg v-else viewBox="0 0 10 10" aria-hidden="true">
          <rect x="0.5" y="0.5" width="9" height="9" rx="1.5" />
        </svg>
      </button>
      <button type="button" class="wbtn wbtn--close" title="关闭" aria-label="关闭" @click="close">
        <svg viewBox="0 0 10 10" aria-hidden="true">
          <path d="M0.5 0.5l9 9M9.5 0.5l-9 9" />
        </svg>
      </button>
    </div>
  </header>
</template>

<style scoped>
/* 这一条就是窗口的标题栏：整块可拖动，上面的按钮用 data-no-drag 排除 */
.tb {
  display: flex;
  align-items: center;
  gap: 12px;
  height: 44px;
  /* 抵消 .app 的左右留白，让窗口按钮贴到窗口右边缘 */
  margin: 0 calc(var(--gutter) * -1);
  padding: 0 var(--gutter);
  user-select: none;
  -webkit-user-select: none;
}

.tb__brand {
  display: flex;
  align-items: center;
  gap: 9px;
  min-width: 0;
}

.tb__mark {
  display: grid;
  place-items: center;
  flex: none;
  width: 22px;
  height: 22px;
  border-radius: 7px;
  box-shadow: 0 1px 2px rgba(13, 17, 23, 0.16), 0 6px 14px -10px rgba(13, 17, 23, 0.7);
}

.tb__mark svg {
  display: block;
  width: 22px;
  height: 22px;
}

/* 品牌字标：等宽、小写，中段「md」用强调色 —— 工具本身就是一条命令，
   等宽字体比衬线/无衬线的常规标题更贴合它 */
.brand {
  display: inline-flex;
  align-items: baseline;
  overflow: hidden;
  color: var(--ink);
  font-family: var(--mono);
  font-size: 13.5px;
  font-weight: 600;
  letter-spacing: -0.01em;
  white-space: nowrap;
}

.brand__dash {
  margin: 0 0.5px;
  color: var(--ink-3);
  font-weight: 400;
}

.brand__word--md {
  color: var(--accent-ink);
}

.tb__status {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-left: auto;
  min-width: 0;
}

.tb__tools {
  display: flex;
  align-items: center;
  gap: 6px;
  flex: none;
}

.tb__sep {
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
  height: 28px;
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

/* 自绘的窗口按钮：46×44 的整块热区，只有悬停才浮起来，关闭键悬停变红 */
.tb__win {
  display: flex;
  align-self: stretch;
  flex: none;
  margin-left: 6px;
  margin-right: calc(var(--gutter) * -1);
}

.wbtn {
  display: grid;
  place-items: center;
  width: 46px;
  border: 0;
  background: transparent;
  color: var(--ink-2);
  cursor: default;
  transition:
    background var(--t-fast) var(--ease),
    color var(--t-fast) var(--ease);
}

.wbtn svg {
  width: 10px;
  height: 10px;
  fill: none;
  stroke: currentColor;
  stroke-width: 1;
}

.wbtn:hover {
  background: color-mix(in srgb, var(--ink) 8%, transparent);
  color: var(--ink);
}

.wbtn:active {
  background: color-mix(in srgb, var(--ink) 15%, transparent);
}

.wbtn--close:hover {
  background: #c42b1c;
  color: #fff;
}

.wbtn--close:active {
  background: #b1271b;
  color: #fff;
}

/* 窄窗口：先丢 PicGo 文字（只留状态点），再窄才丢字标 */
@media (max-width: 820px) {
  .health__text {
    display: none;
  }
}

@media (max-width: 620px) {
  .brand {
    display: none;
  }
}
</style>
