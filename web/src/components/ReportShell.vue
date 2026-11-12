<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import AnimatedNumber from './AnimatedNumber.vue'
import AppIcon from './AppIcon.vue'
import type { IconName } from './AppIcon.vue'
import EmptyState from './EmptyState.vue'
import PageHead from './PageHead.vue'
import { useI18n } from '../i18n'
import type { MessageKey } from '../i18n'

/**
 * 检测 / 下载两页共用的「报告外壳」：页头 + 一行结论 + 粘贴路径 + 可滚动的正文。
 *
 * 只复用外壳，不复用内容 —— 检测页画地址、下载页画「远程 → 本地」，
 * 正文由各自的页面塞进 slot。以前三个作业页共用同一张文件表，
 * 切菜单时画面一模一样，看着像点击没生效（2026-11-11 用户指出）。
 */
const props = defineProps<{
  mode: 'check' | 'download'
  icon: IconName
  tone: 'accent' | 'warn' | 'ok'
  titleKey: MessageKey
  hintKey: MessageKey
  /** 标题右边那句目标信息，例如「只读」「存到 images/」。 */
  tag?: string
  hasRows: boolean
  manualOpen: boolean
  recent?: string[]
  /** 一行结论的几段：4 个地址 · 2 能打开 · 1 打不开。 */
  verdicts: { key: MessageKey; count: number; tone: 'plain' | 'ok' | 'warn' | 'bad' }[]
  /** 这一页正在跑：页头下面那条细线跟着进度走。 */
  busy?: boolean
  progress?: number
  /** 进度说不准（只有一个文件）时，细线改成扫动。 */
  indeterminate?: boolean
}>()

const emit = defineEmits<{
  'pick-files': []
  'pick-folder': []
  'pick-recent': [path: string]
  'submit-paths': [paths: string[]]
  clear: []
  'update:manualOpen': [open: boolean]
}>()

const { t } = useI18n()
const manualText = ref('')
const textarea = ref<HTMLTextAreaElement | null>(null)

watch(
  () => props.manualOpen,
  async (open) => {
    if (!open) return
    await nextTick()
    textarea.value?.focus()
  },
)

function submitManual(): void {
  const paths = manualText.value
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter((line) => line !== '')
  if (paths.length === 0) return
  emit('submit-paths', paths)
  manualText.value = ''
  emit('update:manualOpen', false)
}
</script>

<template>
  <section class="panel report">
    <PageHead
      :icon="icon"
      :tone="tone"
      :title-key="titleKey"
      :lead-key="hintKey"
      :tag="tag"
      :busy="busy"
      :progress="progress"
      :indeterminate="indeterminate"
    >
      <template #actions>
        <button
          type="button"
          class="icon-btn"
          :title="t('queue.pickFiles')"
          :aria-label="t('queue.pickFiles')"
          @click="emit('pick-files')"
        >
          <AppIcon name="file-plus" :size="16" />
        </button>
        <button
          type="button"
          class="icon-btn"
          :title="t('queue.pickFolder')"
          :aria-label="t('queue.pickFolder')"
          @click="emit('pick-folder')"
        >
          <AppIcon name="folder-plus" :size="16" />
        </button>
        <button
          type="button"
          class="icon-btn"
          :class="{ 'icon-btn--active': manualOpen }"
          :title="t('queue.manual.toggle')"
          :aria-label="t('queue.manual.toggle')"
          :aria-pressed="manualOpen"
          @click="emit('update:manualOpen', !manualOpen)"
        >
          <AppIcon name="clipboard" :size="16" />
        </button>

        <span class="report__divider" aria-hidden="true" />

        <button
          type="button"
          class="icon-btn icon-btn--danger"
          :title="t('queue.clear')"
          :aria-label="t('queue.clear')"
          :disabled="!hasRows"
          @click="emit('clear')"
        >
          <AppIcon name="trash" :size="16" />
        </button>
      </template>
    </PageHead>

    <!-- 一行结论：读成一句话，不做成一排卡片 -->
    <p v-if="hasRows && verdicts.length > 0" class="report__summary">
      <template v-for="(cell, index) in verdicts" :key="cell.key">
        <span v-if="index > 0" class="report__sep" aria-hidden="true">·</span>
        <span class="report__stat" :class="`report__stat--${cell.tone}`">
          <AnimatedNumber :value="cell.count" />
          <span class="report__lbl">{{ t(cell.key) }}</span>
        </span>
      </template>
    </p>

    <form v-if="manualOpen" class="manual" @submit.prevent="submitManual">
      <label class="manual__label" for="report-manual">{{ t('queue.manual.label') }}</label>
      <textarea
        id="report-manual"
        ref="textarea"
        v-model="manualText"
        class="textarea"
        rows="3"
        spellcheck="false"
        placeholder="D:\docs\post.md"
        @keydown.enter.exact.prevent="submitManual"
      />
      <div class="manual__foot">
        <span class="manual__hint">{{ t('queue.manual.hint') }}</span>
        <button type="button" class="btn btn--ghost btn--sm" @click="emit('update:manualOpen', false)">
          {{ t('common.cancel') }}
        </button>
        <button type="submit" class="btn btn--secondary btn--sm" :disabled="manualText.trim() === ''">
          {{ t('queue.manual.submit') }}
        </button>
      </div>
    </form>

    <div v-if="hasRows" class="report__body">
      <slot />
    </div>
    <div v-else class="report__empty">
      <EmptyState
        :recent="recent ?? []"
        :remote-mode="mode"
        @pick-files="emit('pick-files')"
        @pick-folder="emit('pick-folder')"
        @pick-recent="emit('pick-recent', $event)"
        @manual="emit('update:manualOpen', true)"
      />
    </div>
  </section>
</template>

<style scoped>
.panel.report {
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.report__divider {
  width: 1px;
  height: 18px;
  margin: 0 4px;
  background: var(--line);
}

.report__summary {
  animation: rise-in 320ms var(--ease-out) 60ms both;
  display: flex;
  flex: none;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 4px 10px;
  margin: 20px 0 0;
  padding: 0 34px;
  color: var(--ink-3);
  font-size: var(--fs-3);
}

.report__stat {
  display: inline-flex;
  align-items: baseline;
  gap: 6px;
}

.report__stat .num {
  color: var(--ink);
  font-size: var(--fs-3);
  font-weight: 600;
}

.report__stat--ok .num {
  color: var(--ok);
}

.report__stat--bad .num {
  color: var(--bad);
}

.report__stat--warn .num {
  color: var(--warn);
}

.report__lbl {
  color: var(--ink-3);
}

.report__sep {
  color: var(--ink-3);
  opacity: 0.7;
}

.report__body {
  flex: 1 1 auto;
  min-height: 0;
  padding: 16px 34px 34px;
  overflow-y: auto;
}

.report__empty {
  display: flex;
  flex: 1 1 auto;
  min-height: 0;
  padding: 16px 34px 34px;
}

.report__empty > * {
  flex: 1;
}

@media (max-width: 900px) {
  .report__summary,
  .report__body {
    padding-inline: 20px;
  }

  .report__body {
    padding-bottom: 24px;
  }
}
</style>