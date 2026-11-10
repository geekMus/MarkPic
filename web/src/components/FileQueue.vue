<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import AppIcon from './AppIcon.vue'
import EmptyState from './EmptyState.vue'
import FileRow from './FileRow.vue'
import type { RunPhase, RunRowView } from '../composables/useRun'
import {
  collectDeadLinks,
  collectMissingLocals,
  copyText,
  formatBrokenReport,
} from '../composables/useDeadLinks'
import { useToasts } from '../composables/useToasts'
import { useI18n } from '../i18n'

const props = defineProps<{
  rows: RunRowView[]
  phase: RunPhase
  completed: number
  total: number
  manualOpen: boolean
  /** 这一轮在做什么（上传 / 只检测 / 下载回本地），只用来给行内文案挑词。 */
  remoteMode?: 'ignore' | 'check' | 'download'
  /** 这一轮是不是被提前掐断的（图床连不上）。 */
  aborted?: boolean
  /** 右栏正在看哪一行；只用来画选中态。 */
  selected?: string | null
}>()

const emit = defineEmits<{
  'pick-files': []
  'pick-folder': []
  'submit-paths': [paths: string[]]
  remove: [id: string]
  reveal: [path: string]
  select: [id: string]
  clear: []
  'update:manualOpen': [open: boolean]
}>()

const { t } = useI18n()
const { success, danger } = useToasts()

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

/**
 * 「只看有问题的」筛选。
 *
 * 检测模式下，一份 300 篇的目录里可能只有十几处死链：全是问题的那几行埋在
 * 几百行「一切正常」中间，翻到手软。这里只做过滤，虚拟化照旧按筛选后的行算，
 * 所以筛选本身不增加渲染成本。
 */
const onlyProblems = ref(false)

function isProblem(row: RunRowView): boolean {
  if (row.missing) return true
  const res = row.result
  if (!res) return false
  // 缺本地图也算「有问题」：检测/下载模式不碰它，可它确实是坏链接。
  if (res.failed > 0 || (res.dead ?? 0) > 0 || (res.localMissing ?? 0) > 0) return true
  return res.status === 'failed' || res.status === 'partial'
}

const problemCount = computed(() => props.rows.reduce((n, row) => (isProblem(row) ? n + 1 : n), 0))

/**
 * 筛选真正生效的条件。problemCount 为 0 时（比如重新跑一轮全好了）自动放开，
 * 否则按钮已经藏起来了，列表却还停在一份空的筛选结果上。
 */
const filtering = computed(() => onlyProblems.value && problemCount.value > 0)

const filteredRows = computed(() =>
  filtering.value ? props.rows.filter((row) => isProblem(row)) : props.rows,
)

/** 切筛选时回到顶部：否则滚动位置还停在上一次的第 200 行，新列表里根本没有那一行。 */
function toggleProblems(): void {
  onlyProblems.value = !onlyProblems.value
  if (body.value) body.value.scrollTop = 0
  scrollTop.value = 0
  void nextTick(() => {
    syncViewport()
    measureMountedRows()
  })
}

/**
 * 失效清单：按地址去重，带每处引用的文件名与行号。
 *
 * 命令行跑完会打一份这样的清单，界面上以前只能一行一行看红字，
 * 想照着改文档就得手抄。这里把它拼成一段 Markdown 复制出去。
 *
 * 页脚要显示的只是「有没有死链」，所以平时只数行上的 dead 计数（O(行数)）；
 * 真正按地址汇总要遍历所有逐图明细（两万张图就是两万次比较），放到点击时做。
 */
const brokenRefs = computed(() =>
  props.rows.reduce(
    (n, row) =>
      n +
      (row.result?.dead ?? 0) +
      (row.result?.remoteFailed ?? 0) +
      (row.result?.localMissing ?? 0),
    0,
  ),
)

async function copyDeadReport(): Promise<void> {
  // 只在点击时遍历明细：两万条 × 每完成一篇重算一次，是白烧 CPU。
  const links = collectDeadLinks(props.rows)
  const missing = collectMissingLocals(props.rows)
  if (links.length === 0 && missing.length === 0) return
  const text = formatBrokenReport(links, missing, {
    title: t('report.dead.title'),
    titleDownload: t('report.dead.titleDownload'),
    summary: (refs, urls) => t('report.dead.summary', { refs, urls }),
    refs: (places, total) => t('report.dead.refs', { refs: places, total }),
    sep: t('report.dead.sep'),
    more: t('report.dead.more'),
    missingTitle: t('report.missing.title'),
    missingSummary: (refs, paths) => t('report.missing.summary', { refs, paths }),
  })
  if (await copyText(text)) {
    const parts: string[] = []
    if (links.length > 0) parts.push(t('queue.copyDead.doneUrls', { urls: links.length }))
    if (missing.length > 0) parts.push(t('queue.copyDead.donePaths', { paths: missing.length }))
    success(parts.join('，'))
    return
  }
  danger(t('queue.copyDead.failed'))
}

/** 每行的高度前缀和，offsets[i] 是第 i 行的顶部偏移。 */
const offsets = computed(() => {
  void heightsVersion.value
  const list_ = filteredRows.value
  const arr = new Float64Array(list_.length + 1)
  for (let i = 0; i < list_.length; i += 1) {
    arr[i + 1] = arr[i] + (heights.get(list_[i].id) ?? ROW_HEIGHT)
  }
  return arr
})

const totalHeight = computed(() => offsets.value[filteredRows.value.length] ?? 0)

function indexAt(offset: number): number {
  const arr = offsets.value
  const count = filteredRows.value.length
  let low = 0
  let high = count
  while (low < high) {
    const mid = (low + high) >> 1
    if (arr[mid + 1] <= offset) low = mid + 1
    else high = mid
  }
  return Math.min(low, Math.max(0, count - 1))
}

const window_ = computed(() => {
  const count = filteredRows.value.length
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

const visibleRows = computed(() => filteredRows.value.slice(window_.value.start, window_.value.end))

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

        <!-- 检测模式下死链往往只有十几处，埋在几百行「一切正常」里翻不到 -->
        <button
          v-if="problemCount > 0"
          type="button"
          class="icon-btn queue__filter"
          :class="{ 'icon-btn--active': onlyProblems }"
          :title="onlyProblems ? t('queue.onlyProblems.off') : t('queue.onlyProblems', { n: problemCount })"
          :aria-label="onlyProblems ? t('queue.onlyProblems.off') : t('queue.onlyProblems', { n: problemCount })"
          :aria-pressed="onlyProblems"
          @click="toggleProblems"
        >
          <AppIcon name="alert" :size="16" />
          <span class="queue__filter-count num" aria-hidden="true">{{ problemCount }}</span>
        </button>

        <!-- 死链要照着改文档：把去重清单复制出去，比一行一行抄省事 -->
        <button
          v-if="brokenRefs > 0"
          type="button"
          class="icon-btn"
          :title="t('queue.copyDead', { refs: brokenRefs })"
          :aria-label="t('queue.copyDead', { refs: brokenRefs })"
          @click="copyDeadReport"
        >
          <AppIcon name="copy" :size="16" />
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
        <!-- 列头与 FileRow 的栅格必须一致：文件 minmax(0,1fr) / 图片 76px / 状态 96px / 操作 68px -->
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
            :remote-mode="remoteMode"
            :aborted="aborted"
            :selected="selected === row.id"
            @remove="emit('remove', $event)"
            @reveal="emit('reveal', $event)"
            @select="emit('select', $event)"
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

/* 「只看有问题的」：图标按钮右上角挂一个计数，一眼知道有几处要看 */
.queue__filter {
  position: relative;
}

.queue__filter-count {
  position: absolute;
  top: -3px;
  right: -3px;
  min-width: 15px;
  height: 15px;
  padding: 0 3px;
  border-radius: 999px;
  background: var(--bad);
  color: #fff;
  font-size: 9.5px;
  font-weight: 600;
  line-height: 15px;
  text-align: center;
  pointer-events: none;
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
  grid-template-columns: minmax(0, 1fr) 76px 96px 68px;
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
  font-weight: 600;
  letter-spacing: 0.04em;
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
    grid-template-columns: minmax(0, 1fr) 96px 68px;
  }

  .ledger__col--num {
    display: none;
  }
}
</style>
