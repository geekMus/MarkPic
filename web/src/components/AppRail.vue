<script setup lang="ts">
import AppIcon from './AppIcon.vue'
import { useI18n, type MessageKey } from '../i18n'

defineProps<{
  /** 当前在哪个视图：转换（队列）或设置。 */
  view: 'convert' | 'settings'
}>()

const emit = defineEmits<{
  navigate: [view: 'convert' | 'settings']
  about: []
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
  <nav class="rail" :aria-label="t('nav.label')">
    <div class="rail__stack">
      <button
        v-for="item in items"
        :key="item.id"
        type="button"
        class="rail__btn"
        :class="{ 'rail__btn--on': view === item.id }"
        :aria-pressed="view === item.id"
        :title="t(item.key)"
        :aria-label="t(item.key)"
        @click="emit('navigate', item.id)"
      >
        <AppIcon :name="item.icon" :size="18" />
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
      </button>
    </div>
  </nav>
</template>

<style scoped>
.rail {
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
  padding: 10px 0;
  border: 1px solid var(--border-panel);
  border-radius: var(--r-5);
  background: linear-gradient(180deg, color-mix(in srgb, var(--panel) 88%, white 12%), var(--panel) 120px);
  box-shadow: var(--shadow-panel);
}

.rail__stack {
  display: flex;
  flex-direction: column;
  gap: 6px;
  align-items: center;
  width: 100%;
}

.rail__stack--foot {
  padding-top: 10px;
  border-top: 1px solid color-mix(in srgb, var(--line) 80%, transparent);
}

.rail__btn {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 38px;
  height: 38px;
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
  background: color-mix(in srgb, var(--panel-2) 72%, transparent);
}

.rail__btn:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}

/* 选中的那一项：淡强调底 + 一条 3px 竖条。竖条画在按钮内侧，不给窗口加任何边线。 */
.rail__btn--on {
  color: var(--accent);
  background: color-mix(in srgb, var(--accent-soft) 88%, transparent);
  border-color: color-mix(in srgb, var(--accent-line) 72%, transparent);
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

@media (max-width: 900px) {
  .rail {
    padding: 8px 0;
  }

  .rail__btn {
    width: 34px;
    height: 34px;
  }
}
</style>