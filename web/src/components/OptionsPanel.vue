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
  <aside class="panel panel--rail">
    <header class="panel__head">
      <div class="panel__title">
        <h2>设置</h2>
        <span v-if="dirty" class="panel__note panel__note--dirty">未保存</span>
      </div>
      <div class="panel__actions">
        <button
          type="button"
          class="icon-btn"
          title="隐藏设置面板"
          aria-label="隐藏设置面板"
          @click="emit('close')"
        >
          <AppIcon name="x" :size="15" />
        </button>
      </div>
    </header>

    <div class="panel__body">
      <template v-if="loaded">
        <section class="section">
          <h3 class="section__title">PicGo 服务</h3>
          <label class="field">
            <span class="field__label">服务地址</span>
            <input
              class="input mono"
              type="text"
              spellcheck="false"
              autocomplete="off"
              :value="settings.endpoint"
              @input="emit('update', { endpoint: textValue($event) })"
            />
            <span class="field__hint">默认 http://127.0.0.1:36677</span>
          </label>
        </section>

        <section class="section">
          <h3 class="section__title">并发与重试</h3>
          <label class="field">
            <span class="field__label">并发数</span>
            <input
              class="input input--short num"
              type="number"
              min="1"
              max="16"
              step="1"
              :value="settings.concurrency"
              @change="emit('update', { concurrency: clampValue($event, 1, 16, 4) })"
            />
            <span class="field__hint">1 – 16</span>
          </label>
          <label class="field">
            <span class="field__label">失败重试</span>
            <input
              class="input input--short num"
              type="number"
              min="0"
              max="5"
              step="1"
              :value="settings.retries"
              @change="emit('update', { retries: clampValue($event, 0, 5, 2) })"
            />
            <span class="field__hint">0 – 5</span>
          </label>
        </section>

        <section class="section">
          <h3 class="section__title">输出方式</h3>
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
              class="input mono"
              type="text"
              spellcheck="false"
              autocomplete="off"
              :value="settings.suffix"
              @input="emit('update', { suffix: textValue($event) })"
            />
            <span class="field__hint">例：post{{ settings.suffix }}.md</span>
          </label>

          <div v-if="settings.output === 'directory'" class="field">
            <span class="field__label">输出目录</span>
            <div class="field__row">
              <input
                class="input mono"
                type="text"
                spellcheck="false"
                autocomplete="off"
                placeholder="D:\output"
                :value="settings.outDir"
                @input="emit('update', { outDir: textValue($event) })"
              />
              <button type="button" class="btn btn--secondary" @click="emit('pick-dir')">
                <AppIcon name="folder-open" :size="14" />
                选择
              </button>
            </div>
          </div>

          <p v-if="settings.output === 'in-place'" class="notice">
            原地替换会直接改写原始文件，请确认已开启备份或已纳入版本控制。
          </p>
        </section>

        <section class="section">
          <h3 class="section__title">写入选项</h3>
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

    <footer v-if="loaded" class="panel__foot">
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
    </footer>
  </aside>
</template>

<style scoped>
.panel__note--dirty {
  color: var(--warn);
  font-weight: 500;
}
</style>
