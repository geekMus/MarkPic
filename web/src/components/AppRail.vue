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
const items: { id: View; icon: IconName; key: MessageKey }[] = [
  { id: 'convert', icon: 'image', key: 'nav.convert' },
  { id: 'check', icon: 'link', key: 'nav.check' },
  { id: 'download', icon: 'download', key: 'nav.download' },
  { id: 'output', icon: 'folder-open', key: 'nav.output' },
  { id: 'settings', icon: 'settings', key: 'nav.settings' },
]

/** 这两个是表单页，共用一份草稿：哪一页都可能是「还没保存」的那一页。 */
const FORM_VIEWS: readonly FormView[] = ['output', 'settings']
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
        :title="t(item.key)"
        :aria-label="t(item.key)"
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

    <div class="rail__stack rail__stack--foot">
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
  justify-content: space-between;
  align-items: stretch;
  gap: 10px;
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

.rail__stack--foot {
  padding-top: 8px;
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
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--fs-2);
  line-height: 1;
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