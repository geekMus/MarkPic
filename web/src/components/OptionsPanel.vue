<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import FormPanel from './FormPanel.vue'
import { OUTPUT_MODES } from '../composables/useSettings'
import { useTheme, type ThemeChoice } from '../composables/useTheme'
import { useI18n, type Lang, type MessageKey } from '../i18n'
import type { Settings } from '../types'

/**
 * 「设置」页：把所有旋钮放在一张单子上。
 *
 * 顺序按一轮作业的走向排：先连上 PicGo（图往哪传）、再定并发与重试，
 * 然后是结果写到哪（文档、下载回来的图、备份与调试模式），最后是界面自己的偏好。
 * 以前「输出」占一个菜单，可它没有任何「切换」的性质，只是设置的另一个章节。
 */
const props = defineProps<{
  settings: Settings
  dirty: boolean
  loaded: boolean
  loading: boolean
  saving: boolean
  loadError: string | null
  /** 当前这一轮会不会上传图片：只有「上传本地图片」且不是调试模式才会。 */
  picgoNeeded: boolean
  /**
   * 调试构建（scripts/dev.mjs）：整轮只扫描、不写文件。
   *
   * 这不是一个开关 —— 发布版里连这个概念都不存在，所以只在 dev 构建里说一句。
   */
  debug: boolean
}>()

const emit = defineEmits<{
  update: [patch: Partial<Settings>]
  save: []
  discard: []
  retry: []
  'pick-dir': []
}>()

const { t, lang, setLang } = useI18n()
const { choice: themeChoice, setChoice: setTheme } = useTheme()

/** 当前选中方式那一句说明，跟在分段控件下面。 */
const outputHintKey = computed<MessageKey>(
  () =>
    OUTPUT_MODES.find(mode => mode.value === props.settings.output)?.hintKey ??
    'settings.output.sidecar.hint',
)

/** 外观是界面自己的偏好，跟转换设置不同，改完立即生效。 */
const THEME_OPTIONS: readonly { value: ThemeChoice; labelKey: MessageKey }[] = [
  { value: 'system', labelKey: 'theme.system' },
  { value: 'light', labelKey: 'theme.light' },
  { value: 'dark', labelKey: 'theme.dark' },
]

const LANG_OPTIONS: readonly { value: Lang; labelKey: MessageKey }[] = [
  { value: 'zh', labelKey: 'language.zh' },
  { value: 'en', labelKey: 'language.en' },
]

/** 叉号行为：没有任务时每次问一句，还是最小化到托盘 / 直接退出；有任务时总会先确认退出。 */
const CLOSE_OPTIONS: readonly { value: Settings['closeAction']; labelKey: MessageKey }[] = [
  { value: 'ask', labelKey: 'settings.closeAction.ask' },
  { value: 'tray', labelKey: 'settings.closeAction.tray' },
  { value: 'exit', labelKey: 'settings.closeAction.exit' },
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
    icon="settings"
    tone="accent"
    lead-key="settings.lead"
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
      <h3 class="section__title">{{ t('output.section.doc') }}</h3>
      <div class="segmented" role="group" :aria-label="t('output.section.doc')">
        <button
          v-for="mode in OUTPUT_MODES"
          :key="mode.value"
          type="button"
          class="segmented__item"
          :aria-pressed="settings.output === mode.value"
          @click="emit('update', { output: mode.value })"
        >
          {{ t(mode.labelKey) }}
        </button>
      </div>
      <p class="check__hint">{{ t(outputHintKey) }}</p>

      <label v-if="settings.output === 'sidecar'" class="field">
        <span class="field__label">{{ t('settings.suffix') }}</span>
        <input
          class="input mono"
          type="text"
          spellcheck="false"
          autocomplete="off"
          :value="settings.suffix"
          @input="emit('update', { suffix: textValue($event) })"
        />
        <span class="field__hint">{{ t('settings.suffix.hint', { suffix: settings.suffix }) }}</span>
      </label>

      <div v-if="settings.output === 'directory'" class="field">
        <span class="field__label">{{ t('settings.outDir') }}</span>
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
            {{ t('common.choose') }}
          </button>
        </div>
      </div>

      <p v-if="settings.output === 'in-place'" class="notice">
        {{ t('settings.inPlace.warning') }}
      </p>
    </section>

    <section class="section">
      <h3 class="section__title">{{ t('output.section.images') }}</h3>
      <label class="field">
        <span class="field__label">{{ t('settings.remoteDir') }}</span>
        <input
          class="input mono"
          type="text"
          spellcheck="false"
          autocomplete="off"
          placeholder="images"
          :value="settings.remoteDir"
          @input="emit('update', { remoteDir: textValue($event) })"
        />
        <span class="field__hint">{{ t('settings.remoteDir.hint') }}</span>
      </label>
    </section>

    <section class="section">
      <h3 class="section__title">{{ t('output.section.write') }}</h3>
      <label class="check">
        <input
          type="checkbox"
          :checked="settings.backup"
          @change="emit('update', { backup: checkedValue($event) })"
        />
        <span>{{ t('settings.backup') }}</span>
      </label>
      <!-- 调试模式没有开关：它是构建时就定下来的，dev 构建里整轮只扫描。 -->
      <p v-if="debug" class="check__hint">{{ t('settings.debugNote') }}</p>
    </section>

    <section class="section">
      <h3 class="section__title">{{ t('settings.section.close') }}</h3>
      <div class="field">
        <span class="field__label">{{ t('settings.closeAction') }}</span>
        <div class="segmented" role="group" :aria-label="t('settings.closeAction')">
          <button
            v-for="opt in CLOSE_OPTIONS"
            :key="opt.value"
            type="button"
            class="segmented__item"
            :aria-pressed="settings.closeAction === opt.value"
            @click="emit('update', { closeAction: opt.value })"
          >
            {{ t(opt.labelKey) }}
          </button>
        </div>
        <span class="field__hint">{{ t('settings.closeAction.hint') }}</span>
      </div>
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