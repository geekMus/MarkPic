<script setup lang="ts">
import AppIcon from './AppIcon.vue'
import { OUTPUT_MODES } from '../composables/useSettings'
import type { Settings } from '../types'

defineProps<{
  settings: Settings
  dirty: boolean
  loaded: boolean
  loading: boolean
  saving: boolean
  loadError: string | null
}>()

const emit = defineEmits<{
  update: [patch: Partial<Settings>]
  save: []
  discard: []
  retry: []
  close: []
  'pick-dir': []
}>()

function textValue(event: Event): string {
  return (event.target as HTMLInputElement).value
}

function checkedValue(event: Event): boolean {
  return (event.target as HTMLInputElement).checked
}

function clampValue(event: Event, min: number, max: number, fallback: number): number {
  const raw = Number((event.target as HTMLInputElement).value)
  const value = Number.isFinite(raw) ? Math.min(max, Math.max(min, Math.round(raw))) : fallback
  ;(event.target as HTMLInputElement).value = String(value)
  return value
}
</script>

<template>
  <aside class="card panel">
    <div class="card__head">
      <div class="card__headline">
        <h2 class="card__title">选项</h2>
      </div>
      <div class="card__actions">
        <button
          type="button"
          class="icon-btn"
          title="隐藏选项面板"
          aria-label="隐藏选项面板"
          @click="emit('close')"
        >
          <AppIcon name="x" :size="15" />
        </button>
      </div>
    </div>

    <div class="card__body panel__body">
      <template v-if="loaded">
        <section class="group">
          <h3 class="group__title">PicGo 服务</h3>
          <label class="field">
            <span class="field__label">服务地址</span>
            <input
              class="input input--mono"
              type="text"
              spellcheck="false"
              autocomplete="off"
              :value="settings.endpoint"
              @input="emit('update', { endpoint: textValue($event) })"
            />
            <span class="field__hint">默认 http://127.0.0.1:36677</span>
          </label>
        </section>

        <section class="group">
          <h3 class="group__title">并发与重试</h3>
          <div class="field-row">
            <label class="field">
              <span class="field__label">并发数</span>
              <input
                class="input num"
                type="number"
                min="1"
                max="16"
                step="1"
                :value="settings.concurrency"
                @change="emit('update', { concurrency: clampValue($event, 1, 16, 4) })"
              />
              <span class="field__hint">1 - 16</span>
            </label>
            <label class="field">
              <span class="field__label">失败重试</span>
              <input
                class="input num"
                type="number"
                min="0"
                max="5"
                step="1"
                :value="settings.retries"
                @change="emit('update', { retries: clampValue($event, 0, 5, 2) })"
              />
              <span class="field__hint">0 - 5</span>
            </label>
          </div>
        </section>

        <section class="group">
          <h3 class="group__title">输出方式</h3>
          <div class="segmented" role="group" aria-label="输出方式">
            <button
              v-for="mode in OUTPUT_MODES"
              :key="mode.value"
              type="button"
              class="segmented__item"
              :aria-pressed="settings.output === mode.value"
              :title="mode.hint"
              @click="emit('update', { output: mode.value })"
            >
              {{ mode.label }}
            </button>
          </div>

          <label v-if="settings.output === 'sidecar'" class="field">
            <span class="field__label">副本后缀</span>
            <input
              class="input input--mono"
              type="text"
              spellcheck="false"
              autocomplete="off"
              :value="settings.suffix"
              @input="emit('update', { suffix: textValue($event) })"
            />
            <span class="field__hint">例：post{{ settings.suffix }}.md</span>
          </label>

          <template v-if="settings.output === 'directory'">
            <div class="field">
              <span class="field__label">输出目录</span>
              <div class="field__row">
                <input
                  class="input input--mono"
                  type="text"
                  spellcheck="false"
                  autocomplete="off"
                  placeholder="D:\output"
                  :value="settings.outDir"
                  @input="emit('update', { outDir: textValue($event) })"
                />
                <button type="button" class="btn btn--secondary btn--sm" @click="emit('pick-dir')">
                  选择
                </button>
              </div>
            </div>
          </template>

          <p v-if="settings.output === 'in-place'" class="notice">
            原地替换会直接改写原始文件，请确认已开启备份或已纳入版本控制。
          </p>
        </section>

        <section class="group">
          <h3 class="group__title">写入选项</h3>
          <label class="check">
            <input
              type="checkbox"
              :checked="settings.backup"
              @change="emit('update', { backup: checkedValue($event) })"
            />
            <span>覆盖前备份原文件</span>
          </label>
          <label class="check">
            <input
              type="checkbox"
              :checked="settings.dryRun"
              @change="emit('update', { dryRun: checkedValue($event) })"
            />
            <span>试运行（只扫描与预览，不写文件、不上传）</span>
          </label>
        </section>
      </template>

      <div v-else class="placeholder">
        <p v-if="loading" role="status" aria-live="polite">正在读取设置…</p>
        <template v-else>
          <p class="placeholder__error">{{ loadError ?? '设置读取失败' }}</p>
          <button type="button" class="btn btn--secondary btn--sm" @click="emit('retry')">
            <AppIcon name="refresh" :size="14" />
            重新读取
          </button>
        </template>
      </div>
    </div>

    <div v-if="loaded" class="panel__foot">
      <button
        type="button"
        class="btn btn--ghost btn--sm"
        :disabled="!dirty || saving"
        @click="emit('discard')"
      >
        还原
      </button>
      <button
        type="button"
        class="btn btn--primary btn--sm"
        :disabled="!dirty || saving"
        @click="emit('save')"
      >
        {{ saving ? '保存中' : '保存设置' }}
      </button>
    </div>
  </aside>
</template>

<style scoped>
.panel__body {
  padding: 12px;
  overflow-y: auto;
}

.group + .group {
  margin-top: 14px;
  padding-top: 13px;
  border-top: 1px solid var(--border);
}

.group__title {
  margin-bottom: 9px;
  color: var(--text-2);
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 5px;
}

.field + .field,
.field-row + .field,
.segmented + .field {
  margin-top: 10px;
}

.field__label {
  color: var(--text-2);
  font-size: 11.5px;
}

.field__hint {
  color: var(--text-3);
  font-size: 11px;
}

.field__row {
  display: flex;
  align-items: center;
  gap: 6px;
}

.field__row .input {
  flex: 1;
  min-width: 0;
}

.field-row {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px;
}

.segmented {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 2px;
  padding: 2px;
  border: 1px solid var(--border);
  border-radius: var(--r-sm);
  background: var(--surface-2);
}

.segmented__item {
  height: 24px;
  padding: 0 4px;
  border: none;
  border-radius: 4px;
  background: transparent;
  color: var(--text-2);
  font-family: inherit;
  font-size: 11.5px;
  cursor: pointer;
  transition:
    background-color var(--dur-hover) var(--ease),
    color var(--dur-hover) var(--ease);
}

.segmented__item:hover {
  color: var(--text-1);
}

.segmented__item[aria-pressed='true'] {
  background: var(--surface);
  color: var(--accent-text);
  font-weight: 600;
  box-shadow: var(--shadow-1);
}

.check {
  display: flex;
  align-items: flex-start;
  gap: 7px;
  color: var(--text-2);
  font-size: 11.5px;
  line-height: 1.5;
  cursor: pointer;
}

.check + .check {
  margin-top: 8px;
}

.check input {
  flex: none;
  width: 13px;
  height: 13px;
  margin: 2px 0 0;
  accent-color: var(--accent);
}

.notice {
  margin-top: 9px;
  padding: 7px 9px;
  border-left: 2px solid var(--warning);
  border-radius: 4px;
  background: var(--warning-soft);
  color: var(--warning);
  font-size: 11px;
  line-height: 1.55;
}

.placeholder {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 9px;
  padding: 6px 2px;
  color: var(--text-3);
  font-size: 11.5px;
}

.placeholder__error {
  color: var(--danger);
  line-height: 1.55;
  overflow-wrap: anywhere;
}

.panel__foot {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 6px;
  flex: none;
  padding: 10px 12px;
  border-top: 1px solid var(--border);
  background: var(--surface-2);
}
</style>
