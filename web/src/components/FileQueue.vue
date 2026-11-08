<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import AppIcon from './AppIcon.vue'
import EmptyState from './EmptyState.vue'
import FileRow from './FileRow.vue'
import type { RunPhase, RunRowView } from '../composables/useRun'
import { useI18n } from '../i18n'

const props = defineProps<{
  rows: RunRowView[]
  phase: RunPhase
  completed: number
  total: number
  manualOpen: boolean
}>()

const emit = defineEmits<{
  'pick-files': []
  'pick-folder': []
  'submit-paths': [paths: string[]]
  remove: [id: string]
  clear: []
  'update:manualOpen': [open: boolean]
}>()

const { t } = useI18n()

const manualText = ref('')
const textarea = ref<HTMLTextAreaElement | null>(null)

const busy = computed(
  () => props.phase === 'preparing' || props.phase === 'running' || props.phase === 'cancelling',
)

watch(
  () => props.manualOpen,
  async (open) => {
    if (!open) return
    await nextTick()
    textarea.value?.focus()
  },
)

function closeManual(): void {
  emit('update:manualOpen', false)
}

function submitManual(): void {
  const paths = manualText.value
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter((line) => line !== '')
  if (paths.length === 0) return
  emit('submit-paths', paths)
  manualText.value = ''
}
</script>

<template>
  <section class="panel queue">
    <header class="panel__head">
      <div class="panel__title">
        <h2>{{ t('queue.title') }}</h2>
        <span v-if="rows.length > 0" class="panel__count num">{{ rows.length }}</span>
        <span v-if="busy" class="panel__note num queue__running" role="status" aria-live="polite">
          {{ t('queue.progress', { done: completed, total }) }}
        </span>
      </div>

      <div class="panel__actions">
        <button
          type="button"
          class="btn btn--secondary btn--sm"
          :title="t('queue.pickFiles')"
          :disabled="busy"
          @click="emit('pick-files')"
        >
          <AppIcon name="file-plus" :size="14" />
          {{ t('queue.files') }}
        </button>
        <button
          type="button"
          class="btn btn--secondary btn--sm"
          :title="t('queue.pickFolder')"
          :disabled="busy"
          @click="emit('pick-folder')"
        >
          <AppIcon name="folder-plus" :size="14" />
          {{ t('queue.folder') }}
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
          :disabled="rows.length === 0 || busy"
          @click="emit('clear')"
        >
          <AppIcon name="trash" :size="16" />
        </button>
      </div>
    </header>

    <div class="panel__body queue__body">
      <form v-if="manualOpen" class="manual" @submit.prevent="submitManual">
        <label class="manual__label" for="queue-manual">
          {{ t('queue.manual.label') }}
        </label>
        <textarea
          id="queue-manual"
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
          <button type="button" class="btn btn--ghost btn--sm" @click="closeManual">
            {{ t('common.cancel') }}
          </button>
          <button type="submit" class="btn btn--secondary btn--sm" :disabled="manualText.trim() === ''">
            {{ t('queue.manual.submit') }}
          </button>
        </div>
      </form>

      <template v-if="rows.length > 0">
        <!-- 列头与 FileRow 的栅格必须一致：文件 minmax(0,1fr) / 图片 76px / 状态 96px / 移除 34px -->
        <div class="ledger__head" aria-hidden="true">
          <span class="ledger__col">{{ t('queue.col.file') }}</span>
          <span class="ledger__col ledger__col--num">{{ t('queue.col.images') }}</span>
          <span class="ledger__col">{{ t('queue.col.status') }}</span>
          <span />
        </div>

        <ul class="queue__list">
          <FileRow
            v-for="row in rows"
            :key="row.id"
            :row="row"
            :busy="busy"
            @remove="emit('remove', $event)"
          />
        </ul>
      </template>

      <EmptyState
        v-else-if="!manualOpen"
        @pick-files="emit('pick-files')"
        @pick-folder="emit('pick-folder')"
        @manual="emit('update:manualOpen', true)"
      />
    </div>
  </section>
</template>

<style scoped>
.queue__body {
  display: flex;
  flex-direction: column;
}

.queue__running {
  color: var(--accent-ink);
}

.queue__list {
  display: flex;
  flex-direction: column;
}

/* 台账列头：跟着滚动吸顶，和行栅格同一套列宽 */
.ledger__head {
  position: sticky;
  top: 0;
  z-index: 2;
  display: grid;
  grid-template-columns: minmax(0, 1fr) 76px 96px 34px;
  align-items: center;
  column-gap: 10px;
  flex: none;
  height: 30px;
  padding: 0 10px 0 36px;
  border-bottom: 1px solid var(--line);
  background: var(--panel-2);
}

.ledger__col {
  color: var(--ink-3);
  font-size: var(--fs-1);
  font-weight: 500;
}

.ledger__col--num {
  text-align: right;
}

.manual {
  display: flex;
  flex-direction: column;
  gap: 9px;
  padding: 14px 16px 15px;
  border-bottom: 1px solid var(--line);
  background: var(--panel-2);
  animation: manual-in var(--t-base) var(--ease);
}

.manual__label {
  color: var(--ink-2);
  font-size: var(--fs-2);
  font-weight: 500;
}

.manual__foot {
  display: flex;
  align-items: center;
  gap: 8px;
}

.manual__hint {
  flex: 1;
  min-width: 0;
  color: var(--ink-3);
  font-size: var(--fs-1);
}

@keyframes manual-in {
  from {
    opacity: 0;
    transform: translateY(-2px);
  }

  to {
    opacity: 1;
    transform: none;
  }
}

/* 窄窗口：行里不再显示「图片」列（列宽留给文件名），列头跟着一起收 */
@media (max-width: 820px) {
  .ledger__head {
    grid-template-columns: minmax(0, 1fr) 96px 34px;
  }

  .ledger__col--num {
    display: none;
  }
}
</style>
