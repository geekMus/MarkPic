<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
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

/* ------------------------------------------------------------------ *
 * 列表虚拟化
 *
 * 5000 篇文档时，把 5000 行全挂进 DOM 会有 15 万个元素：任何一批状态变化
 * 都要重新算样式和布局，实测出现过 4.4 秒的单帧停顿（整张表高 45 万像素）。
 * 这里只挂可视区附近的行，用上下两个占位块撑出滚动高度。
 *
 * 行高不固定（展开逐图明细的行有几百像素），所以按行 id 记实测高度，
 * 没量过的先用折叠高度估算，滚动时用前缀和定位窗口。
 * ------------------------------------------------------------------ */

/** 折叠状态下的行高，第一次测量前的估值。 */
const ROW_HEIGHT = 92
/** 可视区上下各多渲染几行，滚动时不至于露白。 */
const OVERSCAN = 4

const body = ref<HTMLElement | null>(null)
const list = ref<HTMLUListElement | null>(null)
const scrollTop = ref(0)
const viewportHeight = ref(0)
const heights = new Map<string, number>()
/** 实测高度变化后 +1，用来让下面的 computed 重算。 */
const heightsVersion = ref(0)

function measureRow(el: Element): void {
  const id = (el as HTMLElement).dataset.rowId
  if (!id) return
  const height = Math.round(el.getBoundingClientRect().height)
  if (height <= 0) return
  const previous = heights.get(id)
  if (previous !== undefined && Math.abs(previous - height) < 1) return
  heights.set(id, height)
  heightsVersion.value += 1
}

function syncViewport(): void {
  const el = body.value
  if (!el) return
  viewportHeight.value = el.clientHeight
  scrollTop.value = el.scrollTop
}

let observer: ResizeObserver | null = null

onMounted(() => {
  syncViewport()
  observer = new ResizeObserver((entries) => {
    for (const entry of entries) {
      if (entry.target === body.value) {
        viewportHeight.value = body.value?.clientHeight ?? 0
        continue
      }
      measureRow(entry.target)
    }
  })
  if (body.value) observer.observe(body.value)
  measureMountedRows()
})

onBeforeUnmount(() => {
  observer?.disconnect()
  observer = null
})

/** 量一遍当前挂着的行（进可视区的行都要量，展开/收起也会走这里）。 */
function measureMountedRows(): void {
  const items = list.value?.children
  if (!items) return
  for (const item of Array.from(items)) {
    const el = item as HTMLElement
    if (!el.dataset.rowId) continue
    observer?.observe(el)
    measureRow(el)
  }
}

/** 每行的高度前缀和，offsets[i] 是第 i 行的顶部偏移。 */
const offsets = computed(() => {
  void heightsVersion.value
  const arr = new Float64Array(props.rows.length + 1)
  for (let i = 0; i < props.rows.length; i += 1) {
    arr[i + 1] = arr[i] + (heights.get(props.rows[i].id) ?? ROW_HEIGHT)
  }
  return arr
})

const totalHeight = computed(() => offsets.value[props.rows.length] ?? 0)

function indexAt(offset: number): number {
  const arr = offsets.value
  let low = 0
  let high = props.rows.length
  while (low < high) {
    const mid = (low + high) >> 1
    if (arr[mid + 1] <= offset) low = mid + 1
    else high = mid
  }
  return Math.min(low, Math.max(0, props.rows.length - 1))
}

const window_ = computed(() => {
  const count = props.rows.length
  if (count === 0) return { start: 0, end: 0, top: 0, bottom: 0 }
  const height = viewportHeight.value || 600
  const start = Math.max(0, indexAt(scrollTop.value) - OVERSCAN)
  const end = Math.min(count, indexAt(scrollTop.value + height) + 1 + OVERSCAN)
  return {
    start,
    end,
    top: offsets.value[start] ?? 0,
    bottom: Math.max(0, totalHeight.value - (offsets.value[end] ?? totalHeight.value)),
  }
})

const visibleRows = computed(() => props.rows.slice(window_.value.start, window_.value.end))

watch(
  () => props.rows.length,
  async () => {
    await nextTick()
    measureMountedRows()
  },
)

watch(
  () => props.manualOpen,
  async () => {
    await nextTick()
    syncViewport()
  },
)

function onScroll(): void {
  const el = body.value
  if (!el) return
  scrollTop.value = el.scrollTop
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

    <div ref="body" class="panel__body queue__body" @scroll.passive="onScroll">
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

        <ul ref="list" class="queue__list">
          <!-- 只挂可视区附近的行，上下用占位块撑出完整滚动高度 -->
          <li
            v-if="window_.top > 0"
            class="queue__spacer"
            :style="{ height: `${window_.top}px` }"
            aria-hidden="true"
          />
          <FileRow
            v-for="row in visibleRows"
            :key="row.id"
            :data-row-id="row.id"
            :row="row"
            :busy="busy"
            @remove="emit('remove', $event)"
          />
          <li
            v-if="window_.bottom > 0"
            class="queue__spacer"
            :style="{ height: `${window_.bottom}px` }"
            aria-hidden="true"
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

/* 虚拟滚动的占位块：只负责撑高度，不参与布局计算 */
.queue__spacer {
  flex: none;
  list-style: none;
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
