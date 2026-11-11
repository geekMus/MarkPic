<script setup lang="ts">
import FormPanel from './FormPanel.vue'
import { useTheme, type ThemeChoice } from '../composables/useTheme'
import { useI18n, type Lang, type MessageKey } from '../i18n'
import type { Settings } from '../types'

/**
 * 「设置」页：这一轮怎么跑 —— PicGo 在哪、并发多重试几次、界面长什么样。
 *
 * 输出写到哪搬到了「输出」页，远程图片怎么处理变成了侧边栏上的「检测」「下载」。
 * 设置里剩下的每一条都真的只是设置，不再藏着一半的功能。
 */
defineProps<{
  settings: Settings
  dirty: boolean
  loaded: boolean
  loading: boolean
  saving: boolean
  loadError: string | null
  /** 当前这一轮会不会上传图片：只有「转换」且不是试运行才会。 */
  picgoNeeded: boolean
}>()

const emit = defineEmits<{
  update: [patch: Partial<Settings>]
  save: []
  discard: []
  retry: []
}>()

const { t, lang, setLang } = useI18n()
const { choice: themeChoice, setChoice: setTheme } = useTheme()

/** 外观是界面自己的偏好，跟 PicGo / 并发这些转换设置不同，改完立即生效。 */
const THEME_OPTIONS: readonly { value: ThemeChoice; labelKey: MessageKey }[] = [
  { value: 'system', labelKey: 'theme.system' },
  { value: 'light', labelKey: 'theme.light' },
  { value: 'dark', labelKey: 'theme.dark' },
]

const LANG_OPTIONS: readonly { value: Lang; labelKey: MessageKey }[] = [
  { value: 'zh', labelKey: 'language.zh' },
  { value: 'en', labelKey: 'language.en' },
]

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
  <FormPanel
    title-key="settings.title"
    anchor="settings-title"
    :dirty="dirty"
    :loaded="loaded"
    :loading="loading"
    :saving="saving"
    :load-error="loadError"
    @save="emit('save')"
    @discard="emit('discard')"
    @retry="emit('retry')"
  >
    <section class="section">
      <h3 class="section__title">{{ t('settings.section.picgo') }}</h3>
      <label class="field">
        <span class="field__label">{{ t('settings.endpoint') }}</span>
        <input
          class="input mono"
          type="text"
          spellcheck="false"
          autocomplete="off"
          :value="settings.endpoint"
          @input="emit('update', { endpoint: textValue($event) })"
        />
        <span class="field__hint">
          {{ t(picgoNeeded ? 'settings.endpoint.hint' : 'settings.endpoint.optional') }}
        </span>
      </label>
    </section>

    <section class="section">
      <h3 class="section__title">{{ t('settings.section.concurrency') }}</h3>
      <label class="field">
        <span class="field__lblrow">
          <span class="field__label">{{ t('settings.concurrency') }}</span>
          <span class="field__hint">1 – 64</span>
        </span>
        <input
          class="input num"
          type="number"
          min="1"
          max="64"
          step="1"
          :value="settings.concurrency"
          @change="emit('update', { concurrency: clampValue($event, 1, 64, 48) })"
        />
        <span class="check__hint">{{ t('settings.concurrency.hint') }}</span>
      </label>
      <label class="field">
        <span class="field__lblrow">
          <span class="field__label">{{ t('settings.retries') }}</span>
          <span class="field__hint">0 – 5</span>
        </span>
        <input
          class="input num"
          type="number"
          min="0"
          max="5"
          step="1"
          :value="settings.retries"
          @change="emit('update', { retries: clampValue($event, 0, 5, 2) })"
        />
      </label>
      <label class="check">
        <input
          type="checkbox"
          :checked="settings.reuseUploads"
          @change="emit('update', { reuseUploads: checkedValue($event) })"
        />
        <span>
          {{ t('settings.reuse') }}
          <span class="check__hint">{{ t('settings.reuse.hint') }}</span>
        </span>
      </label>
    </section>

    <section class="section">
      <h3 class="section__title">{{ t('settings.section.appearance') }}</h3>
      <div class="field">
        <span class="field__label">{{ t('header.theme.label') }}</span>
        <div class="segmented" role="group" :aria-label="t('header.theme.label')">
          <button
            v-for="opt in THEME_OPTIONS"
            :key="opt.value"
            type="button"
            class="segmented__item"
            :aria-pressed="themeChoice === opt.value"
            @click="setTheme(opt.value)"
          >
            {{ t(opt.labelKey) }}
          </button>
        </div>
      </div>
      <div class="field">
        <span class="field__label">{{ t('language.label') }}</span>
        <div class="segmented" role="group" :aria-label="t('language.label')">
          <button
            v-for="opt in LANG_OPTIONS"
            :key="opt.value"
            type="button"
            class="segmented__item"
            :aria-pressed="lang === opt.value"
            @click="setLang(opt.value)"
          >
            {{ t(opt.labelKey) }}
          </button>
        </div>
      </div>
    </section>
  </FormPanel>
</template>

<style scoped>
/* .field__lblrow 是这一页自己的排版：取值范围写在标签同一行的右端，省掉一行提示 */
.field__lblrow {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
}
</style>