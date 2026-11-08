<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import AppIcon from './AppIcon.vue'
import EmptyState from './EmptyState.vue'
import FileRow from './FileRow.vue'
import type { RunPhase, RunRowView } from '../composables/useRun'

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
  <section class="card queue">
    <div class="card__head">
      <div class="card__headline">
        <h2 class="card__title">文件队列</h2>
        <span v-if="rows.length > 0" class="tag num">{{ rows.length }} 个文件</span>
        <span v-if="busy" class="queue__running num" role="status" aria-live="polite">
          {{ completed }}/{{ total }} 已完成
        </span>
      </div>

      <div class="card__actions">
        <button
          type="button"
          class="icon-btn"
          title="选择文件"
          aria-label="选择文件"
          :disabled="busy"
          @click="emit('pick-files')"
        >
          <AppIcon name="file-plus" :size="15" />
        </button>
        <button
          type="button"
          class="icon-btn"
          title="选择文件夹"
          aria-label="选择文件夹"
          :disabled="busy"
          @click="emit('pick-folder')"
        >
          <AppIcon name="folder-plus" :size="15" />
        </button>
        <button
          type="button"
          class="icon-btn"
          title="手动输入路径"
          aria-label="手动输入路径"
          :aria-pressed="manualOpen"
          @click="emit('update:manualOpen', !manualOpen)"
        >
          <AppIcon name="terminal" :size="15" />
        </button>
        <button
          type="button"
          class="icon-btn"
          title="清空队列"
          aria-label="清空队列"
          :disabled="rows.length === 0 || busy"
          @click="emit('clear')"
        >
          <AppIcon name="trash" :size="15" />
        </button>
      </div>
    </div>

    <div class="card__body queue__body">
      <form v-if="manualOpen" class="manual" @submit.prevent="submitManual">
        <label class="manual__label" for="queue-manual">
          每行一个路径，支持 .md 文件或包含 .md 的文件夹
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
          <span class="manual__hint">Enter 加入队列 · Shift+Enter 换行</span>
          <button type="button" class="btn btn--ghost btn--sm" @click="closeManual">取消</button>
          <button type="submit" class="btn btn--primary btn--sm" :disabled="manualText.trim() === ''">
            加入队列
          </button>
        </div>
      </form>

      <ul v-if="rows.length > 0" class="queue__list">
        <FileRow
          v-for="row in rows"
          :key="row.id"
          :row="row"
          :busy="busy"
          @remove="emit('remove', $event)"
        />
      </ul>

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
.queue__running {
  color: var(--accent-text);
  font-size: 11.5px;
  white-space: nowrap;
}

.queue__body {
  display: flex;
  flex-direction: column;
}

.queue__list {
  display: flex;
  flex-direction: column;
}

.manual {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 10px 12px 12px;
  border-bottom: 1px solid var(--border);
  background: var(--surface-2);
}

.manual__label {
  color: var(--text-2);
  font-size: 11.5px;
}

.manual__foot {
  display: flex;
  align-items: center;
  gap: 8px;
}

.manual__hint {
  flex: 1;
  min-width: 0;
  color: var(--text-3);
  font-size: 11px;
}
</style>
