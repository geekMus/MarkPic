<script setup lang="ts">
import AppIcon from './AppIcon.vue'
import type { IconName } from './AppIcon.vue'
import { useI18n, type MessageKey } from '../i18n'
import type { FormView, View } from '../types'

const props = defineProps<{
  /** 当前在哪个视图：三个作业视图，或输出 / 设置。 */
  view: View
  /** 输出 / 设置里有没有还没保存的改动：有就让那颗按钮带个小点。 */
  dirty?: boolean
  /** 哪个作业页正在跑：在它的图标上点一颗呼吸的小点，切走也看得见。 */
  running?: View | null
}>()

const emit = defineEmits<{
  navigate: [view: View]
  about: []
}>()

const { t } = useI18n()

// 导航项写在一起，后面加功能只要往这里加一条、再到 App 里给一个视图就行；
// 宽度、间距、选中态都不用重新想。
const items: { id: View; icon: IconName; key: MessageKey; desc?: MessageKey }[] = [
  { id: 'convert', icon: 'image', key: 'nav.convert', desc: 'nav.convert.desc' },
  { id: 'download', icon: 'download', key: 'nav.download', desc: 'nav.download.desc' },
  { id: 'check', icon: 'link', key: 'nav.check', desc: 'nav.check.desc' },
  { id: 'settings', icon: 'settings', key: 'nav.settings' },
]

/** 这两个是表单页，共用一份草稿：哪一页都可能是「还没保存」的那一页。 */
const FORM_VIEWS: readonly FormView[] = ['settings']
</script>

<template>
  <nav class="rail" :aria-label="t('nav.label')">
    <div class="rail__stack">
      <button
        v-for="item in items"
        :key="item.id"
        type="button"
        class="rail__btn"
        :data-nav="item.id"
        :class="{ 'rail__btn--on': view === item.id, 'rail__btn--run': running === item.id }"
        :aria-pressed="view === item.id"
        :title="t(item.desc ?? item.key)"
        :aria-label="t(item.desc ?? item.key)"
        @click="emit('navigate', item.id)"
      >
        <AppIcon :name="item.icon" :size="18" />
        <span class="rail__label">{{ t(item.key) }}</span>
        <span
          v-if="dirty && FORM_VIEWS.includes(item.id as FormView) && view !== item.id"
          class="rail__dot"
          :aria-label="t('settings.unsaved')"
        />
      </button>
    </div>

    <!-- 关于不是功能，是入口：贴着「设置」往下放，别让它一个人飘在侧边栏最底。 -->
    <div class="rail__about">
      <button
        type="button"
        class="rail__btn"
        :title="t('nav.about')"
        :aria-label="t('nav.about')"
        @click="emit('about')"
      >
        <AppIcon name="info" :size="17" />
        <span class="rail__label">{{ t('nav.about') }}</span>
      </button>
    </div>
  </nav>
</template>

<style scoped>
.rail__dot {
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: var(--accent);
  flex: none;
}

/* 这一页正在跑：右上角一颗会呼吸的点。切到别的页面也看得见作业没停。 */
.rail__btn--run::after {
  content: '';
  position: absolute;
  top: 50%;
  right: 9px;
  margin-top: -2.5px;
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: var(--accent);
  animation: rail-pulse 1.6s var(--ease) infinite;
}

@keyframes rail-pulse {
  0%,
  100% {
    opacity: 1;
    box-shadow: 0 0 0 0 color-mix(in srgb, var(--accent) 55%, transparent);
  }
  60% {
    opacity: 0.75;
    box-shadow: 0 0 0 4px color-mix(in srgb, var(--accent) 0%, transparent);
  }
}

@media (prefers-reduced-motion: reduce) {
  .rail__btn--run::after {
    animation: none;
  }
}

.rail {
  display: flex;
  flex-direction: column;
  justify-content: flex-start;
  align-items: stretch;
  gap: 16px;
  padding: 4px 2px;
  min-height: 0;
}

.rail__stack {
  display: flex;
  flex-direction: column;
  gap: 6px;
  align-items: stretch;
  width: 100%;
}

/* 一条极淡的分隔线：功能在上面，「关于」在下面 */
.rail__about {
  padding-top: 12px;
  border-top: 1px solid var(--line);
}

/* 「关于」是同一列里的最后一项：和上面的功能项一样占满整行，
   只比它们多一条上方的分隔线，不能缩成一颗内容宽度的药丸。 */
.rail__about .rail__btn {
  width: 100%;
}

.rail__btn {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: flex-start;
  gap: 10px;
  width: auto;
  height: 38px;
  padding: 0 10px;
  border: 1px solid transparent;
  border-radius: var(--r-3);
  color: var(--ink-3);
  transition:
    color var(--t-fast) var(--ease),
    background var(--t-fast) var(--ease),
    border-color var(--t-fast) var(--ease);
}

.rail__btn:hover {
  color: var(--ink);
  background: color-mix(in srgb, var(--ink) 7%, transparent);
}

.rail__btn:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}

/* 选中的那一项：淡强调底 + 一条 3px 竖条。竖条画在按钮内侧，不给窗口加任何边线。 */
.rail__btn--on {
  color: var(--accent);
  background: color-mix(in srgb, var(--accent) 14%, transparent);
  border-color: transparent;
}

.rail__btn--on::before {
  content: '';
  position: absolute;
  left: -1px;
  top: 50%;
  width: 3px;
  height: 17px;
  border-radius: 2px;
  transform: translateY(-50%);
  background: linear-gradient(180deg, var(--accent-vivid), var(--accent));
}

.rail__label {
  white-space: nowrap;
  font-size: var(--fs-3);
  font-weight: 500;
  line-height: 1.35;
}


@media (max-width: 900px) {
  .rail {
    padding: 2px 2px;
  }

  .rail__btn {
    height: 34px;
    padding: 0 8px;
  }
}
</style>