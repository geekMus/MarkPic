<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import AppIcon from './AppIcon.vue'
import type { IconName } from './AppIcon.vue'
import { useI18n } from '../i18n'

export interface PaletteCommand {
  id: string
  label: string
  hint?: string
  icon: IconName
  run: () => void
}

const props = defineProps<{ commands: PaletteCommand[] }>()
const emit = defineEmits<{ close: [] }>()

const { t } = useI18n()
const query = ref('')
const active = ref(0)
const inputEl = ref<HTMLInputElement | null>(null)

// 命令不多（十条上下），只按「名字里包含这段字」过滤就够，
// 不做拼音首字母、不做模糊打分 —— 那些在二十条以内只会让人猜不到为什么某项排前面。
const filtered = computed(() => {
  const needle = query.value.trim().toLowerCase()
  if (needle === '') return props.commands
  return props.commands.filter((cmd) => cmd.label.toLowerCase().includes(needle))
})

watch(filtered, () => {
  active.value = 0
})

function choose(cmd: PaletteCommand | undefined): void {
  if (!cmd) return
  emit('close')
  cmd.run()
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    event.preventDefault()
    emit('close')
    return
  }
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    if (filtered.value.length > 0) active.value = (active.value + 1) % filtered.value.length
    return
  }
  if (event.key === 'ArrowUp') {
    event.preventDefault()
    if (filtered.value.length > 0) {
      active.value = (active.value - 1 + filtered.value.length) % filtered.value.length
    }
    return
  }
  if (event.key === 'Enter') {
    event.preventDefault()
    choose(filtered.value[active.value])
  }
}

onMounted(() => {
  void nextTick(() => inputEl.value?.focus())
})
</script>

<template>
  <div class="cmdk" role="presentation" @mousedown.self="emit('close')">
    <div class="cmdk__panel" role="dialog" aria-modal="true" :aria-label="t('palette.label')">
      <div class="cmdk__search">
        <AppIcon name="search" :size="15" class="cmdk__search-icon" />
        <input
          ref="inputEl"
          v-model="query"
          class="cmdk__input"
          type="text"
          role="combobox"
          aria-expanded="true"
          aria-controls="cmdk-list"
          :placeholder="t('palette.placeholder')"
          spellcheck="false"
          autocomplete="off"
          @keydown="onKeydown"
        />
      </div>

      <ul id="cmdk-list" class="cmdk__list" role="listbox" :aria-label="t('palette.label')">
        <li v-for="(cmd, index) in filtered" :key="cmd.id" role="none">
          <button
            type="button"
            class="cmdk__item"
            role="option"
            :aria-selected="index === active"
            :data-active="index === active ? 'yes' : null"
            @mousedown.prevent="choose(cmd)"
            @mouseenter="active = index"
          >
            <AppIcon :name="cmd.icon" :size="15" class="cmdk__icon" />
            <span class="cmdk__label">{{ cmd.label }}</span>
            <span v-if="cmd.hint" class="cmdk__hint">{{ cmd.hint }}</span>
          </button>
        </li>
        <li v-if="filtered.length === 0" class="cmdk__empty">{{ t('palette.empty') }}</li>
      </ul>

      <div class="cmdk__foot">
        <span class="cmdk__keys">
          <span class="cmdk__key">↑</span>
          <span class="cmdk__key">↓</span>
          {{ t('palette.move') }}
        </span>
        <span class="cmdk__keys">
          <span class="cmdk__key">↵</span>
          {{ t('palette.act') }}
        </span>
        <span class="cmdk__keys">
          <span class="cmdk__key">esc</span>
          {{ t('palette.dismiss') }}
        </span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.cmdk {
  position: fixed;
  z-index: 50;
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding: 12vh 20px 20px;
  background: rgba(4, 6, 10, 0.42);
  inset: 40px 0 0;
  animation: cmdk-in 120ms var(--ease);
}

.cmdk__panel {
  display: flex;
  flex-direction: column;
  width: min(520px, 100%);
  max-height: 60vh;
  overflow: hidden;
  border: 1px solid var(--line-2);
  border-radius: 10px;
  background: var(--panel);
  box-shadow: var(--shadow-pop);
}

.cmdk__search {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 0 12px;
  border-bottom: 1px solid var(--line);
}

.cmdk__search-icon {
  flex: none;
  color: var(--ink-3);
}

.cmdk__input {
  width: 100%;
  height: 44px;
  border: 0;
  background: transparent;
  color: var(--ink);
  font-size: var(--fs-3);
  outline: none;
}

.cmdk__input::placeholder {
  color: var(--ink-3);
}

.cmdk__list {
  margin: 0;
  padding: 6px;
  overflow-y: auto;
  list-style: none;
}

.cmdk__item {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  height: 32px;
  padding: 0 8px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--ink-2);
  font-size: var(--fs-2);
  text-align: left;
  cursor: pointer;
}

.cmdk__item[data-active='yes'] {
  background: var(--panel-2);
  color: var(--ink);
}

.cmdk__icon {
  flex: none;
  color: var(--ink-3);
}

.cmdk__item[data-active='yes'] .cmdk__icon {
  color: var(--ink-2);
}

.cmdk__label {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.cmdk__hint {
  flex: none;
  color: var(--ink-3);
  font-size: var(--fs-1);
}

.cmdk__empty {
  padding: 14px 10px;
  color: var(--ink-3);
  font-size: var(--fs-2);
}

.cmdk__foot {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 8px 12px;
  border-top: 1px solid var(--line);
  color: var(--ink-3);
  font-size: var(--fs-1);
}

.cmdk__keys {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.cmdk__key {
  display: inline-grid;
  min-width: 16px;
  height: 16px;
  padding: 0 4px;
  place-items: center;
  border: 1px solid var(--line-2);
  border-radius: 3px;
  color: var(--ink-3);
  font-size: 10px;
  line-height: 1;
}

@keyframes cmdk-in {
  from {
    opacity: 0;
    transform: translateY(-4px);
  }
}
</style>