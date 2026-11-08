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
  <section class="pane queue">
    <div class="pane__head">
      <div class="pane__headline">
        <h2 class="pane__title">待转换文件</h2>
        <span v-if="rows.length > 0" class="pane__note num">{{ rows.length }} 个文件</span>
        <span v-if="busy" class="pane__note queue__running num" role="status" aria-live="polite">
          {{ completed }}/{{ total }} 已完成
        </span>
      </div>

      <div class="pane__actions">
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

        <span class="queue__sep" aria-hidden="true" />

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

    <div class="pane__body queue__body">
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

      <template v-if="rows.length > 0">
        <!-- 列头与 FileRow 的栅格必须一致：文件 minmax(0,1fr) / 引用 64px / 状态 92px / 移除 30px -->
        <div class="ledger__head" aria-hidden="true">
          <span class="ledger__col">文件</span>
          <span class="ledger__col ledger__col--num">引用</span>
          <span class="ledger__col ledger__col--num">状态</span>
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
.queue__running {
  color: var(--accent-ink);
}

.queue__sep {
  width: 1px;
  height: 16px;
  margin: 0 3px;
  background: var(--line);
}

.queue__body {
  display: flex;
  flex-direction: column;
}

.queue__list {
  display: flex;
  flex-direction: column;
}

/* 台账列头 */
.ledger__head {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 64px 92px 30px;
  align-items: center;
  column-gap: 8px;
  flex: none;
  height: 28px;
  padding: 0 6px 0 37px;
  border-bottom: 1px solid var(--line);
  background: var(--sunk);
}

.ledger__col {
  color: var(--ink-3);
  font-size: 11px;
}

.ledger__col--num {
  text-align: right;
}

.manual {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px 14px;
  border-bottom: 1px solid var(--line);
  background: var(--sunk);
  animation: manual-in var(--t-base) var(--ease);
}

.manual__label {
  color: var(--ink-2);
  font-size: var(--fs-label);
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
  font-size: var(--fs-label);
}

@keyframes manual-in {
  from {
    opacity: 0;
  }
  to {
    opacity: 1;
  }
}
</style>
