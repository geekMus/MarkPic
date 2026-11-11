<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import AppIcon from './AppIcon.vue'
import type { IconName } from './AppIcon.vue'
import EmptyState from './EmptyState.vue'
import { useI18n } from '../i18n'
import type { MessageKey } from '../i18n'

/**
 * 检测 / 下载两页共用的「报告外壳」：标题行 + 一行结论 + 粘贴路径 + 可滚动的正文。
 *
 * 只复用外壳，不复用内容 —— 检测页画地址、下载页画「远程 → 本地」，
 * 正文由各自的页面塞进 slot。以前三个作业页共用同一张文件表，
 * 切菜单时画面一模一样，看着像点击没生效（2026-11-11 用户指出）。
 */
const props = defineProps<{
  mode: 'check' | 'download'
  icon: IconName
  titleKey: MessageKey
  hintKey: MessageKey
  /** 标题右边那句目标信息，例如「只读」「存到 images/」。 */
  tag?: string
  /** 标题旁的数（地址数 / 远程图数）。 */
  count: number
  /** 数的单位，例如「个地址」。 */
  unitKey: MessageKey
  verdicts: { key: MessageKey; count: number; tone: 'plain' | 'ok' | 'warn' | 'bad' }[]
  hasRows: boolean
  manualOpen: boolean
  recent?: string[]
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
  <section class="panel report" :class="`report--${mode}`">
    <header class="panel__head">
      <div class="panel__title">
        <span class="report__mode" :class="`report__mode--${mode}`">
          <AppIcon :name="icon" :size="13" />
        </span>
        <h2>{{ t(titleKey) }}</h2>
        <span v-if="count > 0" class="panel__count num">{{ count }}</span>
        <span v-if="count > 0" class="report__unit">{{ t(unitKey) }}</span>
        <span class="report__sub">{{ t(hintKey) }}</span>
        <span v-if="tag" class="report__tag num">{{ tag }}</span>
      </div>
      <div class="panel__actions">
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
          <AppIcon name="terminal" :size="16" />
        </button>

        <span class="panel__divider" aria-hidden="true" />

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
      </div>
    </header>

    <!-- 一行结论：这一页关心的几个数（地址 / 能打开 / 打不开） -->
    <div v-if="hasRows && verdicts.length > 0" class="report__band">
      <div v-for="cell in verdicts" :key="cell.key" class="report__cell" :class="`report__cell--${cell.tone}`">
        <span class="report__num num">{{ cell.count }}</span>
        <span class="report__lbl">{{ t(cell.key) }}</span>
      </div>
    </div>

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

    <div class="report__body">
      <slot v-if="hasRows" />
      <EmptyState
        v-else
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

.report__mode {
  display: grid;
  flex: none;
  place-items: center;
  width: 21px;
  height: 21px;
  border: 1px solid var(--line);
  border-radius: var(--r-2);
  background: var(--panel-2);
  color: var(--ink-2);
}

.report__mode--check {
  border-color: var(--warn-line);
  background: var(--warn-soft);
  color: var(--warn);
}

.report__mode--download {
  border-color: var(--ok-line);
  background: var(--ok-soft);
  color: var(--ok);
}

.report__unit {
  flex: none;
  color: var(--ink-3);
  font-size: var(--fs-1);
}

.report__sub {
  overflow: hidden;
  min-width: 0;
  color: var(--ink-3);
  font-size: var(--fs-2);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.report__tag {
  flex: none;
  padding: 1px 7px;
  border: 1px solid var(--line);
  border-radius: var(--r-pill);
  background: var(--panel-2);
  color: var(--ink-3);
  font-size: var(--fs-1);
  white-space: nowrap;
}

.report__band {
  display: flex;
  flex: none;
  flex-wrap: wrap;
  gap: 0;
  margin: 12px 18px;
  border: 1px solid var(--line);
  border-radius: var(--r-3);
  background: var(--panel-2);
}

.report__cell {
  display: flex;
  flex: 1 1 0;
  align-items: baseline;
  gap: 8px;
  min-width: 0;
  padding: 10px 15px;
}

.report__cell + .report__cell {
  border-left: 1px solid var(--line);
}

.report__num {
  color: var(--ink);
  font-size: var(--fs-5);
  font-weight: 600;
  line-height: 1.1;
}

.report__lbl {
  overflow: hidden;
  color: var(--ink-3);
  font-size: var(--fs-1);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.report__cell--ok .report__num {
  color: var(--ok);
}

.report__cell--bad .report__num {
  color: var(--bad);
}

.report__cell--warn .report__num {
  color: var(--warn);
}

.report__body {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
}

@media (max-width: 940px) {
  .report__sub {
    display: none;
  }
}
</style>