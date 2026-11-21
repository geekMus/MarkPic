<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import AppIcon from './AppIcon.vue'
import PageHead from './PageHead.vue'
import EmptyState from './EmptyState.vue'
import FileRow from './FileRow.vue'
import type { RunPhase, RunRowView } from '../composables/useRun'
import { queuePageScroll, rememberQueueScroll } from '../composables/useRun'
import {
  collectDeadLinks,
  collectMissingLocals,
  copyText,
  formatBrokenReport,
} from '../composables/useDeadLinks'
import { useToasts } from '../composables/useToasts'
import { plural, useI18n } from '../i18n'

const props = defineProps<{
  rows: RunRowView[]
  phase: RunPhase
  completed: number
  total: number
  /** 这一轮的总进度（0..1，按去重后的图片/地址算）；没给时退回文件完成度。 */
  progress?: number
  /** 这一轮在做什么（上传 / 只检测 / 下载回本地），只用来给行内文案挑词。 */
  remoteMode?: 'ignore' | 'check' | 'download'
  /** 这一轮是不是被提前掐断的（图床连不上）。 */
  aborted?: boolean
  /** 右栏正在看哪一行；只用来画选中态。 */
  selected?: string | null
  /** 最近用过的路径，队列为空时摆在空状态里。 */
  recent?: string[]
}>()

defineExpose({ openSearch })

const emit = defineEmits<{
  'pick-files': []
  'pick-folder': []
  'pick-recent': [path: string]
  remove: [id: string]
  reveal: [path: string]
  select: [id: string]
  clear: []
  'cancel-scan': [id: string]
  'retry-scan': [id: string]
  'confirm-import': [id: string]
}>()

const { t } = useI18n()
const { success, danger } = useToasts()

const busy = computed(
  () => props.phase === 'preparing' || props.phase === 'running' || props.phase === 'cancelling',
)

/** 没有总进度时退回文件完成度：至少别让线一直是 0。 */
const fileProgress = computed(() => (props.total > 0 ? props.completed / props.total : 0))
/** 页头下面那条细线的进度：优先用 App 按图片单元算出来的总进度。 */
const lineProgress = computed(() => props.progress ?? fileProgress.value)
/** 只有没给总进度时才显示文件完成数：有百分比时两个数并排会读成互相矛盾。 */
const showFileProgress = computed(() => props.progress === undefined)




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
const ROW_HEIGHT = 85
/** 可视区上下各多渲染几行，滚动时不至于露白。 */
const OVERSCAN = 4

const body = ref<HTMLElement | null>(null)
const list = ref<HTMLUListElement | null>(null)
const scrollTop = ref(0)
/** 这一屏属于哪一页：滚动位置按页记，切回来接着看上次那一屏。 */
const pageMode = computed(() => props.remoteMode ?? 'ignore')
const viewportHeight = ref(0)
const heights = new Map<string, number>()
/** 实测高度变化后 +1，用来让下面的 computed 重算。 */
const heightsVersion = ref(0)

/**
 * 正在做高度动画的那张卡：这段时间行高每帧都在变，量它就是每帧重算一次
 * 可视区、每帧重排整张列表 —— 收起时那一下「卡」就是它。动画结束再补量。
 */
let animatingRows = 0
let animatingGuard = 0

function resumeMeasuring(): void {
  animatingRows = 0
  window.clearTimeout(animatingGuard)
  animatingGuard = 0
  measureMountedRows()
}

function onRowAnimating(active: boolean): void {
  animatingRows += active ? 1 : -1
  if (animatingRows <= 0) {
    resumeMeasuring()
    return
  }
  // 连点两下会把过渡打断，after-* 不一定回来；留一条兜底，
  // 否则行高测量会一直停着，虚拟列表就再也不动了。
  window.clearTimeout(animatingGuard)
  animatingGuard = window.setTimeout(resumeMeasuring, 600)
}

function measureRow(el: Element): void {
  if (animatingRows > 0) return
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
  // 切回这一页时接着上次的滚动位置（行高是量出来的，所以先量再跳）
  const kept = queuePageScroll(pageMode.value)
  if (kept > 0 && body.value) {
    body.value.scrollTop = kept
    scrollTop.value = kept
    void nextTick(measureMountedRows)
  }
})

watch(scrollTop, (top) => {
  rememberQueueScroll(pageMode.value, top)
})

onBeforeUnmount(() => {
  rememberQueueScroll(pageMode.value, scrollTop.value)
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
  // 扫描没通 / 扫超时 / 被取消：这份文档还什么都没算出来，也是问题。
  const scan = row.scan ?? 'idle'
  if (scan === 'failed' || scan === 'timeout' || scan === 'cancelled') return true
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

const searchOpen = ref(false)
const query = ref('')
const searchInput = ref<HTMLInputElement | null>(null)

const filteredRows = computed(() => {
  const base = filtering.value ? props.rows.filter((row) => isProblem(row)) : props.rows
  const needle = query.value.trim().toLowerCase()
  if (!needle) return base
  return base.filter(
    (row) => row.path.toLowerCase().includes(needle) || row.name.toLowerCase().includes(needle),
  )
})

/** 打开搜索：焦点直接放进输入框，不然还得再点一次。 */
function openSearch(): void {
  searchOpen.value = true
  void nextTick(() => searchInput.value?.focus())
}

/** 关掉搜索时把过滤清掉并回到顶部：留着旧关键字，下次打开会一脸问号。 */
function closeSearch(): void {
  searchOpen.value = false
  query.value = ''
  resetScroll()
}

function onSearchInput(): void {
  resetScroll()
}

function resetScroll(): void {
  if (body.value) body.value.scrollTop = 0
  scrollTop.value = 0
  void nextTick(() => {
    syncViewport()
    measureMountedRows()
  })
}

/** 切筛选时回到顶部：否则滚动位置还停在上一次的第 200 行，新列表里根本没有那一行。 */
function toggleProblems(): void {
  onlyProblems.value = !onlyProblems.value
  resetScroll()
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

/** 队列里还有几张文件夹 / 盘扫描卡片等着确认导入（它们不算任务）。 */
const folderCount = computed(() => Math.max(0, props.rows.length - props.total))

watch(
  () => props.rows.length,
  async () => {
    await nextTick()
    measureMountedRows()
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
    <PageHead
      icon="image"
      tone="accent"
      title-key="page.convert"
      lead-key="view.convert.sub"
      :busy="busy"
      :progress="lineProgress"
    >
      <template #badge>
        <!-- 只数文件卡片：文件夹 / 盘还不是任务，单独写一句「待导入」 -->
        <span v-if="total > 0" class="queue__count num">
          {{ t(plural('queue.count', total), { count: total }) }}
        </span>
        <span v-if="folderCount > 0" class="queue__count num">
          {{ t(plural('footer.folders', folderCount), { count: folderCount }) }}
        </span>
        <span v-if="busy && showFileProgress" class="queue__progress num" role="status" aria-live="polite">
          {{ t('queue.progress', { done: completed, total }) }}
        </span>
      </template>
      <template #actions>
          <button
            type="button"
            class="icon-btn"
            :title="t('queue.pickFiles')"
            :aria-label="t('queue.pickFiles')"
            :disabled="busy"
            @click="emit('pick-files')"
          >
            <AppIcon name="file-plus" :size="16" />
          </button>
          <button
            type="button"
            class="icon-btn"
            :title="t('queue.pickFolder')"
            :aria-label="t('queue.pickFolder')"
            :disabled="busy"
            @click="emit('pick-folder')"
          >
            <AppIcon name="folder-plus" :size="16" />
          </button>
          <!-- 队列几百上千行时，按文件名找比滚轮靠谱 -->
          <input
            v-if="searchOpen"
            ref="searchInput"
            v-model="query"
            class="queue__search"
            type="search"
            :placeholder="t('queue.search.placeholder')"
            :aria-label="t('queue.search')"
            @input="onSearchInput"
            @keydown.esc.stop="closeSearch"
          />
          <button
            v-if="rows.length > 0"
            type="button"
            class="icon-btn"
            :class="{ 'icon-btn--active': searchOpen }"
            :title="t('queue.search')"
            :aria-label="t('queue.search')"
            :aria-pressed="searchOpen"
            @click="searchOpen ? closeSearch() : openSearch()"
          >
            <AppIcon name="search" :size="16" />
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
      
      </template>
    </PageHead>

    <div
      ref="body"
      class="queue__body"
      :class="{ 'panel__body': rows.length > 0 }"
      @scroll.passive="onScroll"
    >
      <ul
        v-if="rows.length > 0"
        ref="list"
        class="queue__list"
      >
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
          @cancel-scan="emit('cancel-scan', $event)"
          @retry-scan="emit('retry-scan', $event)"
          @confirm-import="emit('confirm-import', $event)"
          @animating="onRowAnimating"
        />
        <li
          v-if="window_.bottom > 0"
          class="queue__spacer"
          :style="{ height: `${window_.bottom}px` }"
          aria-hidden="true"
        />
      </ul>

      <EmptyState
        v-else
        :recent="props.recent ?? []"
        :remote-mode="remoteMode"
        @pick-files="emit('pick-files')"
        @pick-folder="emit('pick-folder')"
        @pick-recent="emit('pick-recent', $event)"
      />
    </div>
    <!-- 底栏（统计与主按钮）由页面塞进来：它是这张卡片的脚注，和卡片同宽同底 -->
    <slot name="footer" />
  </section>
</template>

<style scoped>
.queue__search {
  width: 168px;
  height: 26px;
  padding: 0 8px;
  border: 1px solid var(--line);
  border-radius: var(--r-2);
  background: var(--panel-2);
  color: var(--ink);
  font-size: var(--fs-2);
  outline: none;
}

.queue__search:focus {
  border-color: var(--accent);
}

.queue__search::-webkit-search-cancel-button {
  display: none;
}

/* 页头徽标：队列里有多少个文件 / 这一轮跑到哪了 */
.queue__count {
  color: var(--ink-3);
  font-size: var(--fs-2);
  font-weight: 500;
  white-space: nowrap;
}

.queue__progress {
  color: var(--accent-ink);
  font-size: var(--fs-2);
  white-space: nowrap;
}

.panel.queue {
  /* 纵向骨架和检测 / 下载两页一致：页头、手动输入条、列头、正文。
     用 flex 列而不是 grid 行 —— 手动输入条是条件渲染的，行数会变。
     写成 .panel.queue 是为了压过 main.css 里 .panel 那条同样优先级的规则。 */
  display: flex;
  flex-direction: column;
}

.queue__body {
  display: flex;
  flex: 1 1 auto;
  flex-direction: column;
  min-height: 0;
  /* 卡片之间靠每张卡自己的下边距隔开（高度是量出来的，不能依赖 gap）。
     上下留白和检测 / 下载两页的 .report__body 完全一致（16 / 34 / 34）—— 以前这里是
     22 / 34 / 28，从上传切到另外两页时正文会整体上跳 6px，看着像点错了菜单。 */
  padding: 16px 34px 34px;
}

/* 窄窗口和检测 / 下载两页同一档：正文内边距 34 → 20（见 ReportShell.vue 的 900px 断点）。
   空态和卡片列表都在这个容器里，收窄后空态的大标题能从 3 行回到 2 行。 */
/* 空态不挂 .panel__body：不占滚动条槽，居中的空态才不会比检测 / 下载两页左移。 */
@media (max-width: 900px) {
  .queue__body {
    padding-inline: 20px;
    /* 和 .report__body 一样，窄窗口下底部留白也收，多留一行给列表。 */
    padding-bottom: 24px;
  }
}

.queue__running {
  color: var(--accent-ink);
}

.queue__list {
  display: flex;
  flex-direction: column;
  margin: 0;
  padding: 0;
  list-style: none;
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
  color: var(--on-bad);
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

</style>
