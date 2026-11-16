<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import AppIcon from './AppIcon.vue'
import BrandMark from './BrandMark.vue'
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
  /**
   * 调试构建（npm run start --debug 编的那一版）：标题栏挂一枚橙色标记。
   *
   * 默认开发构建和发布版一样，这里恒为 false —— 它不该出现在用户面前。
   */
  debug: boolean
  healthChecking: boolean
  theme: 'light' | 'dark'
  phase: RunPhase
  completed: number
  total: number
}>()

const emit = defineEmits<{
  'refresh-health': []
  'toggle-theme': []
  'open-palette': []
}>()

const { available: windowChrome, maximized, snapHover, snapDown, setSnapTarget, minimize, toggleMaximize, close, startDrag } =
  useWindowControls()

// 最大化键的 DOM：交给 useWindowControls，后端会在这块像素上盖一块原生热区，
// 让 Win11 的贴靠布局浮层照常弹出来（那块像素的鼠标事件再由后端推回来）。
const maxButton = ref<HTMLElement | null>(null)
watch(
  [maxButton, windowChrome],
  ([el, chrome]) => setSnapTarget(chrome ? el : null),
  { immediate: true },
)
const { t } = useI18n()
const { choice: themeChoice } = useTheme()

// 这盏灯回答的是「现在能不能开始」，所以「用不着 PicGo」和「PicGo 掉线」必须分开：
// 只检测 / 只下载远程图时压根不上传，掉线是正常状态，标红只会让人以为出了故障。
//
// 优先用上层算好的那一位：服务端返回的 needed 是按**已保存**的设置算的，
// 而面板里刚改完还没保存时，两者说的不是同一件事。
const needed = computed(() => props.picgoNeeded ?? props.picgo?.needed ?? true)

const picgoTone = computed<'neutral' | 'success' | 'danger'>(() => {
  // 手动刷新时先把灯变灰、文字换成「检查中…」：Ping 很快时如果什么都不变，
  // 用户根本看不出这一下有没有生效。
  if (props.healthChecking) return 'neutral'
  if (props.picgo === null) return 'danger'
  if (props.picgo.online) return 'success'
  return needed.value ? 'danger' : 'neutral'
})

const picgoLabel = computed(() => {
  if (props.healthChecking) return t('header.picgo.checking')
  if (props.picgo === null) return t('header.picgo.offline')
  if (props.picgo.online) return t('header.picgo.online')
  return needed.value ? t('header.picgo.offline') : t('header.picgo.notNeeded')
})

const picgoTitle = computed(() => {
  if (props.healthChecking) return t('header.picgo.checkingHint')
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

</script>

<template>
  <!-- 双击有两条路：startDrag 在按下时就自己判（手抖、慢一点也算），
       原生 dblclick 兜系统判出来的那种标准双击；两条一起命中只切一次。 -->
  <header class="tb" @mousedown="startDrag">
    <div class="tb__brand">
      <span class="tb__mark">
        <!-- 标记跟着主题换配色：亮色用浅底那版（和 exe / favicon 的默认图标一致），
             暗色用深底那版。几何与 assets/mark-{light,dark}.svg 逐字相同。 -->
        <BrandMark :theme="props.theme" />
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
      <!-- 调试构建的标记：release 构建里 debug 恒 false，这块根本不渲染。 -->
      <span v-if="debug" class="tb__debug" :title="t('debug.title')">
        <span class="tb__debug-dot" aria-hidden="true" />
        {{ t('debug.badge') }}
      </span>
      <StatusPill v-if="runVisible" :label="runLabel" tone="accent" pulse />
    </div>

    <div class="tb__tools" data-no-drag>
      <button
        type="button"
        class="health"
        :data-tone="picgoTone"
        :data-busy="healthChecking ? 'true' : undefined"
        :title="picgoTitle"
        :disabled="healthChecking"
        :aria-busy="healthChecking"
        @click="emit('refresh-health')"
      >
        <span class="health__dot" aria-hidden="true" />
        <span class="health__text">{{ picgoLabel }}</span>
      </button>

      <button
        type="button"
        class="icon-btn"
        :title="themeTitle"
        :aria-label="themeTitle"
        @click="emit('toggle-theme')"
      >
        <AppIcon :name="themeIcon" :size="15" />
      </button>
    </div>

    <!-- 窗口按钮只在桌面窗口里出现；浏览器里没有系统标题栏可替代，直接隐藏 -->
    <div v-if="windowChrome" class="tb__win" data-no-drag>
      <button type="button" class="wbtn" :aria-label="t('header.window.minimize')" @click="minimize">
        <svg viewBox="0 0 10 10" aria-hidden="true">
          <path d="M0.5 5.5h9" />
        </svg>
        <span class="wbtn__tip" aria-hidden="true">{{ t('header.window.minimize') }}</span>
      </button>
      <button
        ref="maxButton"
        type="button"
        class="wbtn wbtn--max"
        :class="{ 'is-hover': snapHover, 'is-active': snapDown }"
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
        <span class="wbtn__tip" aria-hidden="true">{{ maximized ? t('header.window.restore') : t('header.window.maximize') }}</span>
      </button>
      <button type="button" class="wbtn wbtn--close" :aria-label="t('common.close')" @click="close">
        <svg viewBox="0 0 10 10" aria-hidden="true">
          <path d="M0.5 0.5l9 9M9.5 0.5l-9 9" />
        </svg>
        <span class="wbtn__tip" aria-hidden="true">{{ t('common.close') }}</span>
      </button>
    </div>
  </header>
</template>

<style scoped>
/* 这一条就是窗口的标题栏：整块可拖动，上面的按钮用 data-no-drag 排除 */
.tb {
  display: flex;
  align-items: center;
  gap: 10px;
  height: 40px;
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
  width: 22px;
  height: 22px;
  border-radius: 6px;
  /* 方片是 22px、字标墨迹只有 16px 左右，两边 flex 居中只保证「盒子」对齐：
     实测方片中心 19.6px、字标墨迹中心 20.55px（标题栏 40px，正中间是 20），
     于是方片看着比字标高一截。这里把方片压下去 0.5px、字标提上来 0.5px，
     两个墨迹中心都落回 20px —— 数字改了就一起改 .brand 的 translateY。 */
  transform: translateY(0.5px);
}

.tb__mark svg {
  display: block;
  width: 22px;
  height: 22px;
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
  font-size: 24px;
  line-height: 1.1;
  letter-spacing: 0.004em;
  font-variant-ligatures: discretionary-ligatures;
  white-space: nowrap;
  /* 墨迹居中而不是行盒居中：Gabriola 的 ascent/descent 是 18/9，「MarkPic」又没有下伸部，
     行盒居中之后墨迹整体偏高，跟左边的小方块并排时看起来像「底边对齐」。
     0.164em 让墨迹中心落回方块中心（跟着字号缩放）；比原来的 0.185em 少 0.5px，
     因为方块也往下压了 0.5px，两边一起把中心对到标题栏正中。 */
  transform: translateY(0.164em);
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

/* 调试构建的标记：橙色胶囊 + 一颗实心点，和右侧的 PicGo 状态灯区分开。
   发布版没有这一块（health.debug 恒为 false）。 */
.tb__debug {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 8px 2px 7px;
  border: 1px solid var(--warn-line);
  border-radius: var(--r-pill);
  background: var(--warn-soft);
  color: var(--warn);
  font-size: 11px;
  font-weight: 600;
  line-height: 1.45;
  white-space: nowrap;
  -webkit-app-region: no-drag;
}

.tb__debug-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}

.tb__tools {
  display: flex;
  align-items: center;
  gap: 6px;
  flex: none;
}

/* PicGo 状态读成一个可点的徽标，悬停才浮起来 */
.health {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  height: 24px;
  padding: 0 9px;
  border: 1px solid var(--line-2);
  border-radius: var(--r-pill);
  background: transparent;
  color: var(--ink-3);
  font-size: var(--fs-1);
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

.health:disabled {
  cursor: progress;
}

.health:disabled:hover {
  border-color: var(--line-2);
  background: transparent;
  box-shadow: none;
}

.health__dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--ink-3);
}

/* 检测中：小圆点一呼一吸，配合「检查中…」告诉用户这一下已经点到了 */
.health[data-busy='true'] .health__dot {
  animation: health-pulse 1s var(--ease) infinite;
}

@keyframes health-pulse {
  0%,
  100% {
    opacity: 0.35;
    transform: scale(0.8);
  }
  50% {
    opacity: 1;
    transform: scale(1.15);
  }
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

/* 自绘的窗口按钮：46 宽、和标题栏等高的一块热区。
   观感照 Windows 自己的标题栏来：默认完全透明，只有悬停才浮起一层极淡的底色，
   按下再深一档，关闭键悬停变红。悬停淡到 5% 是有意的 —— 系统那三个按钮就这么淡，
   做重了立刻不像窗口的一部分，像网页上的按钮。 */
.tb__win {
  display: flex;
  align-self: stretch;
  flex: none;
  margin-left: 6px;
  margin-right: calc(var(--gutter) * -1);
}

.wbtn {
  position: relative;
  display: grid;
  place-items: center;
  width: 46px;
  border: 0;
  background: transparent;
  color: var(--ink-2);
  cursor: default;
  /* 不做过场：系统的标题栏按钮是瞬时变色，带渐变的悬停一眼就看得出是网页做的。 */
  transition: none;
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

/* 最大化键的悬停态由原生热区回报（后端推 __markpicSnap 过来）：
   那块像素被一个原生子窗口盖着，鼠标不再经过网页，:hover / :active 都收不到，
   只能照着同样的观感补一份。 */
.wbtn--max.is-hover {
  background: color-mix(in srgb, var(--ink) 8%, transparent);
  color: var(--ink);
}

.wbtn--max.is-active {
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

/* 悬停提示：和 VSCode 的窗口按钮一样，停够一小会儿才出来，不是一碰就弹。
   最大化键被原生热区盖着，网页收不到 :hover，所以它由 is-hover（后端推来的
   enter 事件）点亮；三个键共用同一块提示，位置和观感才不会两套。
   用 absolute 而不是原生 title：原生提示框在深色界面里还是浅色的，且它和
   这块自绘标题栏的圆角/字号对不上。 */
.wbtn__tip {
  position: absolute;
  top: calc(100% + 6px);
  left: 50%;
  z-index: 30;
  padding: 5px 9px;
  border: 1px solid var(--line-2);
  border-radius: 6px;
  background: var(--panel-2);
  box-shadow: 0 4px 14px rgba(9, 13, 20, 0.18);
  color: var(--ink);
  font-family: var(--font);
  font-size: var(--fs-2);
  font-weight: 400;
  line-height: 1.35;
  white-space: nowrap;
  pointer-events: none;
  opacity: 0;
  transform: translateX(-50%);
  transition: opacity 0.08s var(--ease);
}

.wbtn--close .wbtn__tip {
  /* 贴到窗口右缘会像被裁掉，留 4px 呼吸；左/中两键仍是居中。 */
  right: 4px;
  left: auto;
  transform: none;
}

.wbtn:hover .wbtn__tip,
.wbtn--max.is-hover .wbtn__tip {
  opacity: 1;
  /* 悬停 450ms 后再淡入：和系统/VSCode 的提示节奏一致，快速划过不会闪一片。 */
  transition-delay: 0.45s;
}

@media (prefers-reduced-motion: reduce) {
  .wbtn__tip {
    transition: none;
  }
}

@media (prefers-reduced-motion: reduce) {
  .health[data-busy='true'] .health__dot {
    animation: none;
    opacity: 0.7;
  }
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
