<script setup lang="ts">
import AppIcon from './AppIcon.vue'
import { useI18n, type MessageKey } from '../i18n'

const props = defineProps<{
  /** 当前在哪个视图：转换（队列）或设置。 */
  view: 'convert' | 'settings'
  /** 侧边栏形态：只有图标的窄条，或图标 + 文字的导航。 */
  mode: 'compact' | 'expanded'
  /** 设置里有没有还没保存的改动：有就让设置那颗按钮带个小点。 */
  dirty?: boolean
}>()

const emit = defineEmits<{
  navigate: [view: 'convert' | 'settings']
  about: []
  toggle: []
}>()

const { t } = useI18n()

// 导航项写在一起，后面加功能（历史、图床、批量任务…）只要往这里加一条、再到 App 里
// 给一个视图就行；宽度、间距、选中态都不用重新想。
const items: { id: 'convert' | 'settings'; icon: 'image' | 'settings'; key: MessageKey }[] = [
  { id: 'convert', icon: 'image', key: 'nav.convert' },
  { id: 'settings', icon: 'settings', key: 'nav.settings' },
]
</script>

<template>
  <nav class="rail" :class="{ 'rail--expanded': props.mode === 'expanded' }" :aria-label="t('nav.label')">
    <div class="rail__stack">
      <button
        v-for="item in items"
        :key="item.id"
        type="button"
        class="rail__btn"
        :data-nav="item.id"
        :class="{ 'rail__btn--on': view === item.id }"
        :aria-pressed="view === item.id"
        :title="t(item.key)"
        :aria-label="t(item.key)"
        @click="emit('navigate', item.id)"
      >
        <AppIcon :name="item.icon" :size="18" />
        <span v-if="props.mode === 'expanded'" class="rail__label">{{ t(item.key) }}</span>
        <span
          v-if="item.id === 'settings' && props.dirty && view !== 'settings'"
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
        <span v-if="props.mode === 'expanded'" class="rail__label">{{ t('nav.about') }}</span>
      </button>

      <!-- 展开 / 收起：一个按钮两个方向，图标跟着当前形态走 -->
      <button
        type="button"
        class="rail__btn rail__btn--toggle"
        :title="props.mode === 'expanded' ? t('nav.collapse') : t('nav.expand')"
        :aria-label="props.mode === 'expanded' ? t('nav.collapse') : t('nav.expand')"
        :aria-expanded="props.mode === 'expanded'"
        @click="emit('toggle')"
      >
        <AppIcon :name="props.mode === 'expanded' ? 'chevrons-left' : 'chevrons-right'" :size="17" />
        <span v-if="props.mode === 'expanded'" class="rail__label">{{ t('nav.collapse') }}</span>
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

.rail {
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
  padding: 4px 0;
  min-height: 0;
}

.rail--expanded {
  align-items: stretch;
  padding: 4px 2px;
}

.rail__stack {
  display: flex;
  flex-direction: column;
  gap: 6px;
  align-items: center;
  width: 100%;
}

.rail--expanded .rail__stack {
  align-items: stretch;
}

.rail__stack--foot {
  padding-top: 8px;
}

.rail__btn {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  width: 38px;
  height: 38px;
  padding: 0;
  border: 1px solid transparent;
  border-radius: var(--r-3);
  color: var(--ink-3);
  transition:
    color var(--t-fast) var(--ease),
    background var(--t-fast) var(--ease),
    border-color var(--t-fast) var(--ease);
}

.rail--expanded .rail__btn {
  width: auto;
  justify-content: flex-start;
  padding: 0 10px;
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

/* 收起/展开本身不是「当前在哪」，不跟着选中态变色 */
.rail__btn--toggle {
  color: var(--ink-3);
}

@media (max-width: 900px) {
  .rail {
    padding: 2px 0;
  }

  .rail--expanded {
    padding: 2px 2px;
  }

  .rail__btn {
    width: 34px;
    height: 34px;
  }

  .rail--expanded .rail__btn {
    width: auto;
    padding: 0 8px;
  }
}
</style>