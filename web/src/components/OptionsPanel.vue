<script setup lang="ts">
import AppIcon from './AppIcon.vue'
import { computed } from 'vue'
import { OUTPUT_MODES, REMOTE_MODES } from '../composables/useSettings'
import { useTheme, type ThemeChoice } from '../composables/useTheme'
import { useI18n, type Lang, type MessageKey } from '../i18n'
import type { Settings } from '../types'

const props = defineProps<{
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

const { t, lang, setLang } = useI18n()
const { choice: themeChoice, setChoice: setTheme } = useTheme()

/** 外观是界面自己的偏好，跟 PicGo / 输出那些转换设置不同，改完立即生效。 */
const THEME_OPTIONS: readonly { value: ThemeChoice; labelKey: MessageKey }[] = [
  { value: 'system', labelKey: 'theme.system' },
  { value: 'light', labelKey: 'theme.light' },
  { value: 'dark', labelKey: 'theme.dark' },
]

/** 当前选中模式那一句说明，跟在分段控件下面。 */
const remoteHintKey = computed<MessageKey>(
  () => REMOTE_MODES.find(mode => mode.value === props.settings.remote)?.hintKey ?? 'settings.remote.ignore.hint',
)

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
  <!-- 设置是弹窗，不再挤占队列的宽度；遮罩避开标题栏，窗口仍可拖动 -->
  <div class="dlg" @mousedown.self="emit('close')">
    <section
      class="dlg__panel"
      role="dialog"
      aria-modal="true"
      aria-labelledby="settings-title"
      @keydown.esc.stop="emit('close')"
    >
      <header class="panel__head">
        <div class="panel__title">
          <h2 id="settings-title">{{ t('settings.title') }}</h2>
          <span v-if="dirty" class="panel__note panel__note--dirty">{{ t('settings.unsaved') }}</span>
        </div>
        <div class="panel__actions">
          <button
            type="button"
            class="icon-btn"
            :title="t('settings.close')"
            :aria-label="t('settings.close')"
            @click="emit('close')"
          >
            <AppIcon name="x" :size="15" />
          </button>
        </div>
      </header>

      <div class="dlg__body">
        <template v-if="loaded">
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
              <span class="field__hint">{{ t('settings.endpoint.hint') }}</span>
            </label>
          </section>

          <section class="section">
            <h3 class="section__title">{{ t('settings.section.remote') }}</h3>
            <div class="segmented" role="group" :aria-label="t('settings.section.remote')">
              <button
                v-for="mode in REMOTE_MODES"
                :key="mode.value"
                type="button"
                class="segmented__item"
                :aria-pressed="settings.remote === mode.value"
                :title="t(mode.hintKey)"
                @click="emit('update', { remote: mode.value })"
              >
                {{ t(mode.labelKey) }}
              </button>
            </div>
            <p class="check__hint">{{ t(remoteHintKey) }}</p>

            <label v-if="settings.remote === 'download'" class="field">
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
            <h3 class="section__title">{{ t('settings.section.concurrency') }}</h3>
            <label class="field">
              <span class="field__lblrow">
                <span class="field__label">{{ t('settings.concurrency') }}</span>
                <span class="field__hint">1 – 32</span>
              </span>
              <input
                class="input num"
                type="number"
                min="1"
                max="32"
                step="1"
                :value="settings.concurrency"
                @change="emit('update', { concurrency: clampValue($event, 1, 32, 16) })"
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
            <h3 class="section__title">{{ t('settings.section.output') }}</h3>
            <div class="segmented" role="group" :aria-label="t('settings.section.output')">
              <button
                v-for="mode in OUTPUT_MODES"
                :key="mode.value"
                type="button"
                class="segmented__item"
                :aria-pressed="settings.output === mode.value"
                :title="t(mode.hintKey)"
                @click="emit('update', { output: mode.value })"
              >
                {{ t(mode.labelKey) }}
              </button>
            </div>

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
            <h3 class="section__title">{{ t('settings.section.write') }}</h3>
            <label class="check">
              <input
                type="checkbox"
                :checked="settings.backup"
                @change="emit('update', { backup: checkedValue($event) })"
              />
              <span>{{ t('settings.backup') }}</span>
            </label>
            <label class="check">
              <input
                type="checkbox"
                :checked="settings.dryRun"
                @change="emit('update', { dryRun: checkedValue($event) })"
              />
              <span>{{ t('settings.dryRun') }}</span>
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
        </template>

        <div v-else class="placeholder">
          <p v-if="loading" role="status" aria-live="polite">{{ t('settings.loading') }}</p>
          <template v-else>
            <p class="placeholder__error">{{ loadError ?? t('settings.loadFailed') }}</p>
            <button type="button" class="btn btn--secondary btn--sm" @click="emit('retry')">
              <AppIcon name="refresh" :size="14" />
              {{ t('common.reload') }}
            </button>
          </template>
        </div>
      </div>

      <footer v-if="loaded" class="panel__foot">
        <span class="dlg__kbd">
          <span class="kbd">Ctrl</span>
          <span class="kbd">Enter</span>
          {{ t('settings.shortcut.save') }}
        </span>
        <button
          type="button"
          class="btn btn--ghost btn--sm"
          :disabled="!dirty || saving"
          @click="emit('discard')"
        >
          {{ t('settings.discard') }}
        </button>
        <button
          type="button"
          class="btn btn--primary btn--sm"
          :disabled="!dirty || saving"
          @click="emit('save')"
        >
          {{ saving ? t('settings.saving') : t('settings.save') }}
        </button>
      </footer>
    </section>
  </div>
</template>

<style scoped>
/* .dlg / .dlg__panel / .dlg__body 是全局原语（见 main.css），这里只留弹窗自己的排版 */

/* 弹窗里不必像侧栏那样松，收紧一点让四个分组尽量一屏放下 */
.dlg__body > .section {
  padding: 14px 18px 15px;
}

.dlg__body > .section:first-child {
  border-top: 0;
}

/* 取值范围写在标签同一行的右端，省掉一行提示 */
.field__lblrow {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
}

.dlg__kbd {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin-right: auto;
  color: var(--ink-3);
  font-size: var(--fs-1);
}

.panel__note--dirty {
  color: var(--warn);
  font-weight: 500;
}
</style>
