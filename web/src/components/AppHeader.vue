<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import StatusPill from './StatusPill.vue'
import { useTheme } from '../composables/useTheme'
import { useWindowControls } from '../composables/useWindowControls'
import { useI18n } from '../i18n'
import type { Health } from '../types'
import type { RunPhase } from '../composables/useRun'

const props = defineProps<{
  picgo: Health['picgo'] | null
  // 这一轮到底用不用得着 PicGo，由面板里那份设置决定（可能还没保存）。
  picgoNeeded?: boolean
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
const { t } = useI18n()
const { choice: themeChoice } = useTheme()

// 这盏灯回答的是「现在能不能开始」，所以「用不着 PicGo」和「PicGo 掉线」必须分开：
// 只检测 / 只下载远程图时压根不上传，掉线是正常状态，标红只会让人以为出了故障。
//
// 优先用上层算好的那一位：服务端返回的 needed 是按**已保存**的设置算的，
// 而面板里刚改完还没保存时，两者说的不是同一件事。
const needed = computed(() => props.picgoNeeded ?? props.picgo?.needed ?? true)

const picgoTone = computed<'neutral' | 'success' | 'danger'>(() => {
  if (props.picgo === null) return props.healthChecking ? 'neutral' : 'danger'
  if (props.picgo.online) return 'success'
  return needed.value ? 'danger' : 'neutral'
})

const picgoLabel = computed(() => {
  if (props.picgo === null) {
    return props.healthChecking ? t('header.picgo.checking') : t('header.picgo.offline')
  }
  if (props.picgo.online) return t('header.picgo.online')
  return needed.value ? t('header.picgo.offline') : t('header.picgo.notNeeded')
})

const picgoTitle = computed(() => {
  const detail = props.picgo?.detail.trim() ?? ''
  return detail === '' ? t('header.picgo.recheck') : t('header.picgo.recheckDetail', { detail })
})

const runVisible = computed(
  () => props.phase === 'preparing' || props.phase === 'running' || props.phase === 'cancelling',
)

const runLabel = computed(() => {
  if (props.phase === 'preparing') return t('header.run.preparing')
  if (props.phase === 'cancelling') return t('header.run.cancelling')
  return t('header.run.progress', { done: props.completed, total: props.total })
})

/** 三种主题各有自己的图标：跟随系统用显示器，亮色太阳，暗色月亮。 */
const themeIcon = computed<'sun' | 'moon' | 'monitor'>(() => {
  if (themeChoice.value === 'system') return 'monitor'
  return props.theme === 'dark' ? 'moon' : 'sun'
})

const themeTitle = computed(() => {
  const names = { system: t('theme.system'), light: t('theme.light'), dark: t('theme.dark') }
  return t('theme.switch', { name: names[themeChoice.value] })
})

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
        <!-- 标记：深蓝圆角方块里一座「M 形山」+ 一颗暖色太阳。山脊的两个峰同时是
             MarkPic 的 M；和 assets/icon.ico、web/index.html 的 favicon 是同一套几何
             （方块 1.25..22.75 r6.2 / 山 3.4-19.3 起 / 太阳 17.15,6.95,1.62），改一处要同步三处。 -->
        <svg viewBox="0 0 24 24" fill="none">
          <defs>
            <linearGradient id="mark-bg" x1="3" y1="1" x2="21" y2="23" gradientUnits="userSpaceOnUse">
              <stop stop-color="#22304d" />
              <stop offset=".58" stop-color="#131c30" />
              <stop offset="1" stop-color="#0a0f1c" />
            </linearGradient>
            <linearGradient id="mark-ridge" x1="4" y1="19" x2="20" y2="9" gradientUnits="userSpaceOnUse">
              <stop stop-color="#1b8ea6" />
              <stop offset=".55" stop-color="#3fd4e8" />
              <stop offset="1" stop-color="#c8f8ff" />
            </linearGradient>
          </defs>
          <rect x="1.25" y="1.25" width="21.5" height="21.5" rx="6.2" fill="url(#mark-bg)" />
          <ellipse cx="11.6" cy="16.4" rx="9" ry="5.4" fill="#2fd0e4" opacity=".18" />
          <circle cx="17.15" cy="6.95" r="1.62" fill="#ffd9a8" />
          <path d="M3.4 19.3 7.6 11 12 15.4 16.6 10.6 20.6 19.3Z" fill="url(#mark-ridge)" opacity=".92" />
          <path d="M3.4 19.3h17.2" stroke="#fff" stroke-opacity=".16" stroke-width="1.15" stroke-linecap="round" />
          <rect x="1.25" y="1.25" width="21.5" height="21.5" rx="6.2" fill="none" stroke="#fff" stroke-opacity=".09" stroke-width=".9" />
        </svg>
      </span>
      <!-- 字标：名字还是命令行里那个，但不带连字符 —— 连字符在 13px 下会读成三个碎词，
           换成两颗小圆点做节奏；md 仍是唯一被点亮的那段（它是这个工具真正在做的事）。
           正文用界面字体，不再用等宽：等宽放在标题栏里像一句代码注释。 -->
      <span class="brand" role="img" aria-label="MarkPic" :title="t('header.brand')">
        <span class="brand__mark" aria-hidden="true">Mark</span>
        <span class="brand__pic" aria-hidden="true">Pic</span>
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
        <AppIcon :name="themeIcon" :size="16" />
      </button>

      <button
        type="button"
        class="icon-btn"
        :class="{ 'icon-btn--active': optionsOpen }"
        :title="optionsOpen ? t('header.settings.close') : t('header.settings.open')"
        :aria-label="optionsOpen ? t('header.settings.close') : t('header.settings.open')"
        :aria-pressed="optionsOpen"
        @click="emit('toggle-settings')"
      >
        <AppIcon name="settings" :size="16" />
      </button>
    </div>

    <!-- 窗口按钮只在桌面窗口里出现；浏览器里没有系统标题栏可替代，直接隐藏 -->
    <div v-if="windowChrome" class="tb__win" data-no-drag>
      <button type="button" class="wbtn" :title="t('header.window.minimize')" :aria-label="t('header.window.minimize')" @click="minimize">
        <svg viewBox="0 0 10 10" aria-hidden="true">
          <path d="M0.5 5.5h9" />
        </svg>
      </button>
      <button
        type="button"
        class="wbtn"
        :title="maximized ? t('header.window.restore') : t('header.window.maximize')"
        :aria-label="maximized ? t('header.window.restore') : t('header.window.maximize')"
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
      <button type="button" class="wbtn wbtn--close" :title="t('common.close')" :aria-label="t('common.close')" @click="close">
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
  /* 一层从上沉下来的极淡底色 + 一条 hairline：标题栏才有「框」的厚度。
     它不是窗口边线，是窗口内部的分区。 */
  background: linear-gradient(180deg, color-mix(in srgb, var(--panel) 46%, transparent), transparent 82%);
  border-bottom: 1px solid color-mix(in srgb, var(--line) 76%, transparent);
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
  width: 24px;
  height: 24px;
  border-radius: 8px;
  /* 深色标题栏里 --mark 和底色几乎同值，靠这圈极淡的强调色描边把形状托住；
     浅色下这条描边看不见，也不影响 */
  outline: 1px solid color-mix(in srgb, var(--accent) 24%, transparent);
  outline-offset: -1px;
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.14), 0 1px 2px rgba(13, 17, 23, 0.18),
    0 8px 18px -12px rgba(13, 17, 23, 0.8);
}

.tb__mark svg {
  display: block;
  width: 24px;
  height: 24px;
}

/* 字标用 Gabriola：Windows 自带的书法体，笔画有粗细与连势，比等宽小写更像一个名字。
   换机器没这字体时依次退回 Segoe Script、Ink Free、Palatino，都缺才落到 Georgia。 */
.brand {
  display: inline-flex;
  align-items: baseline;
  gap: 2px;
  min-width: 0;
  color: var(--ink);
  font-family: Gabriola, "Segoe Script", "Ink Free", "Palatino Linotype", Georgia, serif;
  font-size: 27px;
  line-height: 1.12;
  letter-spacing: 0.004em;
  font-variant-ligatures: discretionary-ligatures;
  white-space: nowrap;
  /* 墨迹居中而不是行盒居中：Gabriola 的 ascent/descent 是 18/9，「MarkPic」又没有下伸部，
     行盒居中之后墨迹整体偏高约 5px，跟左边 24px 的标记并排时看起来像「底边对齐」。
     挪 0.185em（27px 字号下正好 5px，跟着字号缩放）让墨迹中心落在标记中心上。 */
  transform: translateY(0.185em);
}

.brand__mark {
  flex: none;
}

/* 「Pic」是这个词里唯一被点亮的一段：工具真正在做的事是「图」 */
.brand__pic {
  flex: none;
  margin-left: 1px;
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
