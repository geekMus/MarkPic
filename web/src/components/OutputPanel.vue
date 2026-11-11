<script setup lang="ts">
import AppIcon from './AppIcon.vue'
import FormPanel from './FormPanel.vue'
import { computed } from 'vue'
import { OUTPUT_MODES } from '../composables/useSettings'
import { useI18n, type MessageKey } from '../i18n'
import type { Settings } from '../types'

/**
 * 「输出」页：这一轮的东西都写到哪 —— 文档、下载回来的图片、备份。
 *
 * 以前这些散在设置里，和 PicGo 地址、并发数混在一张单子上；现在按菜单拆开：
 * 这一页只回答「结果落在磁盘的哪里」，设置页只回答「怎么跑」。
 */
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
  'pick-dir': []
}>()

const { t } = useI18n()

/** 当前选中方式那一句说明，跟在分段控件下面。 */
const outputHintKey = computed<MessageKey>(
  () =>
    OUTPUT_MODES.find(mode => mode.value === props.settings.output)?.hintKey ??
    'settings.output.sidecar.hint',
)

function textValue(event: Event): string {
  return (event.target as HTMLInputElement).value
}

function checkedValue(event: Event): boolean {
  return (event.target as HTMLInputElement).checked
}
</script>

<template>
  <FormPanel
    title-key="output.title"
    anchor="output-title"
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
            placeholder="D:\\output"
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
      <label class="check">
        <input
          type="checkbox"
          :checked="settings.dryRun"
          @change="emit('update', { dryRun: checkedValue($event) })"
        />
        <span>
          {{ t('settings.dryRun') }}
          <span class="check__hint">{{ t('output.dryRun.hint') }}</span>
        </span>
      </label>
    </section>
  </FormPanel>
</template>