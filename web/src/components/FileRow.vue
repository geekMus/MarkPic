<script setup lang="ts">
import { computed, nextTick, ref, useId } from 'vue'
import AppIcon from './AppIcon.vue'
import StatusPill from './StatusPill.vue'
import type { FileStatus, ImageItem, ImageStatus } from '../types'
import type { RunRowView } from '../composables/useRun'
import { useI18n, type MessageKey } from '../i18n'

type Tone = 'neutral' | 'accent' | 'success' | 'warning' | 'danger'

const props = defineProps<{ row: RunRowView; busy: boolean }>()

const emit = defineEmits<{ remove: [id: string] }>()

const { t } = useI18n()

const FILE_STATUS_META: Record<FileStatus, { tone: Tone; labelKey: MessageKey }> = {
  ok: { tone: 'success', labelKey: 'row.status.done' },
  partial: { tone: 'warning', labelKey: 'row.status.partial' },
  'no-images': { tone: 'neutral', labelKey: 'row.status.noImages' },
  failed: { tone: 'danger', labelKey: 'row.status.failed' },
  cancelled: { tone: 'warning', labelKey: 'row.status.cancelled' },
  preview: { tone: 'neutral', labelKey: 'row.status.preview' },
  checked: { tone: 'neutral', labelKey: 'row.status.checked' },
}

const IMAGE_STATUS_META: Record<ImageStatus, { tone: Tone; labelKey: MessageKey }> = {
  uploaded: { tone: 'success', labelKey: 'row.status.uploaded' },
  reused: { tone: 'accent', labelKey: 'row.status.reused' },
  cached: { tone: 'accent', labelKey: 'row.status.cached' },
  remote: { tone: 'neutral', labelKey: 'row.status.remote' },
  missing: { tone: 'warning', labelKey: 'row.status.missing' },
  failed: { tone: 'danger', labelKey: 'row.status.uploadFailed' },
  cancelled: { tone: 'neutral', labelKey: 'row.status.cancelled' },
  pending: { tone: 'neutral', labelKey: 'row.status.pending' },
  localized: { tone: 'success', labelKey: 'row.status.localized' },
  'local-kept': { tone: 'neutral', labelKey: 'row.status.localKept' },
  'remote-ok': { tone: 'success', labelKey: 'row.status.remoteOk' },
  'remote-bad': { tone: 'danger', labelKey: 'row.status.remoteBad' },
}

const expanded = ref(false)

const detailsId = `file-row-${useId()}`

const statusMeta = computed<{ tone: Tone; labelKey: MessageKey }>(() => {
  const result = props.row.result
  if (result) {
    // 检测模式下整篇「已检测」读起来是成功，但有死链的行必须一眼认出来 ——
    // 尤其点开「只看有问题的」之后，一行行全是中性的「已检测」等于没筛。
    if ((result.dead ?? 0) > 0) return { tone: 'danger', labelKey: 'row.status.checked' }
    // 缺本地图的行只算警告（不红），但它确实有问题，筛选得能看见。
    if ((result.localMissing ?? 0) > 0) {
      return { tone: 'warning', labelKey: FILE_STATUS_META[result.status].labelKey }
    }
    return FILE_STATUS_META[result.status]
  }
  if (props.row.phase === 'running') return { tone: 'accent', labelKey: 'row.status.uploading' }
  if (props.row.phase === 'cancelled') return { tone: 'warning', labelKey: 'row.status.cancelled' }
  if (props.row.phase === 'settled') return { tone: 'neutral', labelKey: 'row.status.finished' }
  if (props.row.missing) return { tone: 'neutral', labelKey: 'row.status.notFound' }
  return { tone: 'neutral', labelKey: 'row.status.queued' }
})

const countLabel = computed(() => {
  const images = props.row.result?.images ?? props.row.images
  if (images === null || images === undefined) {
    return props.row.missing ? '—' : t('row.count.pending')
  }
  return t('row.count', { count: images })
})

const reason = computed(() => {
  const result = props.row.result
  if (!result) {
    if (props.row.phase === 'cancelled') return t('row.reason.cancelled')
    if (props.row.missing) return t('row.reason.noFiles')
    return ''
  }
  if (result.error) return result.error
  if (result.failed > 0) {
    // 「没下载回来」和「本地图片读不出来」是两回事：都写成「未处理」的话，
    // 用户不知道是去改文档里的地址，还是去看磁盘。
    const remote = result.remoteFailed ?? 0
    if (remote > 0 && remote === result.failed) {
      return t('row.reason.remoteFailed', { count: remote })
    }
    return t('row.reason.failed', { count: result.failed })
  }
  const dead = result.dead ?? 0
  if (dead > 0) return t('row.reason.dead', { count: dead })
  // 检测/下载模式不碰本地图，但缺图是文档自己的毛病，不单列会看着像一切正常。
  if (localMissingCount.value > 0) {
    return t('row.reason.localMissing', { count: localMissingCount.value })
  }
  const warning = result.warnings?.[0]
  return warning ?? ''
})

const percent = computed(() => {
  if (props.row.phase === 'settled' || props.row.phase === 'cancelled') return 100
  if (props.row.total > 0) {
    return Math.min(100, Math.round((props.row.uploaded / props.row.total) * 100))
  }
  return 0
})

const indeterminate = computed(
  () => props.row.phase === 'running' && props.row.total === 0 && props.row.result === null,
)

/**
 * 进度轨只在「还没跑完」的时候出现。跑完之后整行宽的一条彩色细线会被误读成
 * 分隔线，结论交给状态列的那颗点就够了。
 */
const barVisible = computed(() => props.row.phase === 'running' || props.row.phase === 'queued')

const items = computed<ImageItem[]>(() => props.row.result?.items ?? [])

/**
 * 同一张图在本轮已经上传过、这里直接复用地址的引用数。
 * 注意 uploaded 是「指向图床的引用数」（含复用），skipped 是「跳过的引用数」
 * （网络图片等），两者都不是上传请求数，所以分开显示。
 */
const reusedCount = computed(
  () => items.value.filter(item => item.status === 'reused' || item.status === 'cached').length,
)

/** 预检模式下所有待上传的引用都还没有真正上传，单独数一遍，别混进「跳过」。 */
const pendingCount = computed(() => items.value.filter(item => item.status === 'pending').length)

/** 下载回本地：有几张远程图落到了磁盘上。 */
const localizedCount = computed(() => items.value.filter(item => item.status === 'localized').length)

/**
 * 上面那个数按引用处算：同一张图在这篇文档里被引用 20 次就是 20，
 * 可 images/ 里只有一个文件。这一行要显示的是「下成了几个文件」。
 */
const localizedUniqueCount = computed(() => {
  const seen = new Set<string>()
  for (const item of items.value) {
    if (item.status !== 'localized') continue
    seen.add(item.url ?? item.target)
  }
  return seen.size
})

/**
 * 「本地保留」里有一部分其实找不到：检测/下载模式不碰本地图，但引用坏掉的
 * 文件名还是要数出来，否则一行「本地保留 3」看着像三张图都在。
 */
const localMissingCount = computed(() => items.value.filter(item => item.status === 'local-kept' && item.error).length)

/** 下载模式下本地图片原样留着，它们既不是「替换」也不是「跳过」。 */
const localKeptCount = computed(
  () => items.value.filter(item => item.status === 'local-kept' && !item.error).length,
)

/** 碰不了的远程地址（ftp://、data:）：单独数，别混进「跳过」里当没事发生。 */
const remoteUnsupportedCount = computed(
  () => items.value.filter(item => item.status === 'remote' && item.error).length,
)

/** 后端把 pending 也算进 skipped（那是给命令行汇总用的），展示时要把它们剥离出来。 */
const skippedOnly = computed(() =>
  Math.max(
    0,
    (props.row.result?.skipped ?? 0) -
      pendingCount.value -
      localKeptCount.value -
      localMissingCount.value,
  ),
)

/**
 * 「替换」这个数在只碰远程图的模式里是句废话（永远是 0），
 * 那种时候把版面让给「下载」「本地保留」「检测」。
 */
const remoteSeenCount = computed(
  () => items.value.filter(item => item.status === 'remote-ok' || item.status === 'remote-bad').length,
)

const showReplacedStat = computed(
  () =>
    (props.row.result?.uploaded ?? 0) > 0 ||
    (pendingCount.value === 0 &&
      localizedCount.value === 0 &&
      localKeptCount.value === 0 &&
      remoteSeenCount.value === 0 &&
      remoteUnsupportedCount.value === 0),
)

/**
 * 展开后没有明细可列时说的话，要跟这一行的实际处境对得上。
 * （路径根本没匹配到文件的行不展开，见 noDetails，所以这里只剩「没有本地图片」一种情况。）
 */
const emptyDetailsText = computed(() => t('row.details.empty'))

/** 没有逐图明细的行（例如路径下压根没有 Markdown）就不该给一个能点的展开箭头。 */
const noDetails = computed(() => Boolean(props.row.missing) && !props.row.result)

/**
 * 逐图状态文字。下载模式里「failed」指的是下载不下来，
 * 一律说成「上传失败」会让人以为是自己把图传丢了。
 */
function itemLabel(item: ImageItem): string {
  if (item.status === 'failed' && /^https?:\/\//i.test(item.target)) {
    return t('row.status.downloadFailed')
  }
  // remote 状态在保留原样的模式里是「本来就是网络图片」，在检测/下载模式里
  // 却是「这种地址我们碰不了」（ftp://、data:）——后者带上 error，别混为一谈。
  if (item.status === 'remote' && item.error) {
    return t('row.status.remoteUnsupported')
  }
  // 同一档里「保留原样」和「引用的文件根本不在」是两件事，标签得分开。
  if (item.status === 'local-kept' && item.error) {
    return t('row.status.localMissing')
  }
  return t(IMAGE_STATUS_META[item.status].labelKey)
}

function itemDetail(item: ImageItem): string {
  if (item.status === 'failed') return item.error ?? t('row.status.uploadFailed')
  if (item.status === 'missing') return item.error ?? t('row.item.missingFile')
  // 有 error 就是「这个地址处理不了」，原因得说出来；没有才是「本来就是远程图」。
  if (item.status === 'remote') return item.error ?? t('row.item.remoteSkipped')
  // 本地保留的行里，带 error 的是「引用的文件根本不在」，原因得说出来。
  if (item.status === 'local-kept' && item.error) return item.error
  // 远程相关的状态：有错误就说错误，没有就显示下载后的本地相对路径
  if (item.status === 'localized') return item.url ?? ''
  if (item.status === 'remote-bad') return item.error ?? t('row.status.remoteBad')
  if (item.status === 'remote-ok') return item.url ?? ''
  if (item.status === 'local-kept') return t('row.item.localKept')
  // 待上传/已取消的引用：状态列已经写了，这里再重复一句就是噪音
  if (item.status === 'pending' || item.status === 'cancelled') return ''
  return item.url ?? ''
}

/* ------------------------------------------------------------------ *
 * 逐图明细的虚拟滚动
 *
 * 一篇两万张图的文档，展开就是两万行：实测 184164 个 DOM 节点、95 MB 堆、
 * 展开要 2.06 秒，而用户真正能看见的永远只有那十几行。超过阈值就只渲染
 * 可视区附近的行，用上下两个占位块撑出滚动高度。
 *
 * 明细行高度是固定的（单行 + 省略号，实测 30px），所以不需要像文件列表
 * 那样按行 id 记实测高度。
 * ------------------------------------------------------------------ */

/** 超过这个条数才启用虚拟滚动：几百行全渲染反而更简单，也不会有滚动条。 */
const VIRTUAL_THRESHOLD = 200
/** 明细行高度（`.items__row` 的 min-height 30px，单行不换行）。 */
const ITEM_HEIGHT = 30
/** 可视区上下各多渲染几行，滚动时不至于露白。 */
const ITEM_OVERSCAN = 8
/** 虚拟滚动时明细区的最大高度：再高就把整页滚动条抢走了。 */
const ITEM_VIEWPORT = 420

const itemsEl = ref<HTMLElement | null>(null)
const itemsScrollTop = ref(0)
const itemsViewport = ref(ITEM_VIEWPORT)

const virtualItems = computed(() => items.value.length > VIRTUAL_THRESHOLD)

const itemWindow = computed(() => {
  const total = items.value.length
  if (!virtualItems.value) {
    return { start: 0, end: total, padTop: 0, padBottom: 0 }
  }
  const height = itemsViewport.value > 0 ? itemsViewport.value : ITEM_VIEWPORT
  const visible = Math.ceil(height / ITEM_HEIGHT) + ITEM_OVERSCAN * 2
  let start = Math.floor(itemsScrollTop.value / ITEM_HEIGHT) - ITEM_OVERSCAN
  if (start < 0) start = 0
  let end = start + visible
  if (end > total) end = total
  return { start, end, padTop: start * ITEM_HEIGHT, padBottom: (total - end) * ITEM_HEIGHT }
})

const visibleItems = computed(() =>
  items.value.slice(itemWindow.value.start, itemWindow.value.end),
)

function onItemsScroll(event: Event): void {
  const el = event.target as HTMLElement
  itemsScrollTop.value = el.scrollTop
  if (el.clientHeight > 0) itemsViewport.value = el.clientHeight
}

function toggleExpanded(): void {
  expanded.value = !expanded.value
  if (expanded.value) {
    itemsScrollTop.value = 0
    void nextTick(() => {
      const el = itemsEl.value
      if (!el) return
      el.scrollTop = 0
      if (el.clientHeight > 0) itemsViewport.value = el.clientHeight
    })
  }
}

/** 只有真正改写过的引用才配得上一条强调色的替换箭头。 */
function replaced(status: ImageStatus): boolean {
  return (
    status === 'uploaded' ||
    status === 'reused' ||
    status === 'cached' ||
    status === 'localized'
  )
}
</script>

<template>
  <li class="item" :data-tone="statusMeta.tone">
    <div class="item__main">
      <button
        type="button"
        class="item__toggle"
        :disabled="noDetails"
        :aria-expanded="noDetails ? undefined : expanded"
        :aria-controls="noDetails ? undefined : detailsId"
        :title="noDetails ? row.path : expanded ? t('row.details.collapse') : t('row.details.expand')"
        @click="toggleExpanded"
      >
        <span
          v-if="!noDetails"
          class="item__chev"
          :class="{ 'item__chev--on': expanded }"
          aria-hidden="true"
        >
          <AppIcon name="chevron-down" :size="14" />
        </span>

        <span class="item__id">
          <span class="item__name">{{ row.name }}</span>
          <span class="item__dir mono" :title="row.path">{{ row.dir }}</span>
        </span>
      </button>

      <span class="item__count num">{{ countLabel }}</span>

      <span class="item__status">
        <StatusPill :label="t(statusMeta.labelKey)" :tone="statusMeta.tone" compact />
      </span>

      <button
        type="button"
        class="icon-btn icon-btn--sm icon-btn--danger item__remove"
        :title="t('row.remove')"
        :aria-label="t('row.remove')"
        :disabled="busy"
        @click="emit('remove', row.id)"
      >
        <AppIcon name="x" :size="13" />
      </button>
    </div>

    <p v-if="reason" class="item__reason">{{ reason }}</p>

    <div v-if="barVisible" class="item__rail" :class="{ 'item__rail--wait': indeterminate }">
      <span :style="indeterminate ? undefined : { width: percent + '%' }" />
    </div>

    <div v-if="expanded && !noDetails" :id="detailsId" class="details">
      <div class="details__stats">
        <!-- 预检时「替换 0」是句废话，等真有引用被改写再报这个数 -->
        <span v-if="showReplacedStat" class="stat">
          <span class="stat__n num">{{ row.result?.uploaded ?? 0 }}</span>
          <span class="stat__k">{{ t('row.stat.uploaded') }}</span>
        </span>
        <span v-if="localizedCount > 0" class="stat">
          <span class="stat__n num">{{ localizedUniqueCount }}</span>
          <span class="stat__k">{{ t('row.stat.localized') }}</span>
        </span>
        <span v-if="localKeptCount > 0" class="stat">
          <span class="stat__n num">{{ localKeptCount }}</span>
          <span class="stat__k">{{ t('row.stat.localKept') }}</span>
        </span>
        <span v-if="reusedCount > 0" class="stat">
          <span class="stat__n num">{{ reusedCount }}</span>
          <span class="stat__k">{{ t('row.stat.reused') }}</span>
        </span>
        <span v-if="pendingCount > 0" class="stat">
          <span class="stat__n num">{{ pendingCount }}</span>
          <span class="stat__k">{{ t('row.stat.pending') }}</span>
        </span>
        <span v-if="remoteUnsupportedCount > 0" class="stat">
          <span class="stat__n num">{{ remoteUnsupportedCount }}</span>
          <span class="stat__k">{{ t('row.stat.remoteUnsupported') }}</span>
        </span>
        <span v-if="skippedOnly > 0" class="stat">
          <span class="stat__n num">{{ skippedOnly }}</span>
          <span class="stat__k">{{ t('row.stat.skipped') }}</span>
        </span>
        <span v-if="(row.result?.failed ?? 0) > 0" class="stat">
          <span class="stat__n num stat__n--bad">{{ row.result?.failed }}</span>
          <span class="stat__k">{{ t('row.stat.failed') }}</span>
        </span>
      </div>

      <ul
        v-if="items.length > 0"
        ref="itemsEl"
        class="items"
        :class="{ 'items--virtual': virtualItems }"
        :style="virtualItems ? { maxHeight: ITEM_VIEWPORT + 'px' } : undefined"
        @scroll="onItemsScroll"
      >
        <li v-if="itemWindow.padTop > 0" class="items__pad" :style="{ height: itemWindow.padTop + 'px' }" aria-hidden="true" />
        <li
          v-for="(item, index) in visibleItems"
          :key="itemWindow.start + index"
          class="items__row"
          :data-status="item.status"
        >
          <span class="items__line mono">L{{ item.line }}</span>
          <span class="items__status">{{ itemLabel(item) }}</span>
          <span class="items__target mono" :title="item.absolutePath || item.target">
            {{ item.target }}
          </span>
          <span v-if="replaced(item.status)" class="items__arrow items__arrow--on" aria-hidden="true">
            <AppIcon name="chevron-down" :size="12" class="items__arrow-icon" />
          </span>
          <!-- 没被改写的引用不给箭头：单独一个灰箭头飘在那里只会被当成噪点 -->
          <span v-else class="items__arrow" aria-hidden="true" />
          <span class="items__result mono" :title="itemDetail(item)">{{ itemDetail(item) }}</span>
        </li>
        <li
          v-if="itemWindow.padBottom > 0"
          class="items__pad"
          :style="{ height: itemWindow.padBottom + 'px' }"
          aria-hidden="true"
        />
      </ul>

      <p v-else class="details__empty">{{ emptyDetailsText }}</p>

      <p v-if="row.result?.outputPath" class="details__out">
        <span>{{ t('row.output') }}</span>
        <span class="mono">{{ row.result.outputPath }}</span>
      </p>

      <ul v-if="row.result?.warnings?.length" class="warnings">
        <li v-for="(warning, index) in row.result.warnings" :key="index">{{ warning }}</li>
      </ul>
    </div>
  </li>
</template>

<style scoped>
.item {
  position: relative;
  padding-left: 10px;
  border-bottom: 1px solid var(--line);
  transition: background var(--t-fast) var(--ease);
}

.item:last-child {
  border-bottom: 0;
}

.item:hover {
  background: color-mix(in srgb, var(--panel-2) 60%, transparent);
}

.item__main {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 76px 96px 34px;
  align-items: center;
  column-gap: 10px;
  min-height: 62px;
  padding-right: 10px;
}

/* 展开箭头绝对定位在轨道左侧，名字文字和列头的「文件」严格对齐 */
.item__toggle:disabled {
  cursor: default;
}

.item__toggle {
  position: relative;
  display: flex;
  align-items: center;
  min-width: 0;
  padding: 8px 0 8px 26px;
  border: 0;
  background: none;
  text-align: left;
  cursor: pointer;
}

.item__chev {
  position: absolute;
  left: 4px;
  top: 50%;
  display: grid;
  place-items: center;
  width: 16px;
  height: 16px;
  margin-top: -8px;
  color: var(--ink-3);
  transition: transform var(--t-fast) var(--ease), color var(--t-fast) var(--ease);
}

.item__chev--on {
  transform: rotate(180deg);
  color: var(--ink-2);
}

.item__toggle:hover .item__chev {
  color: var(--ink);
}

.item__id {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
}

.item__name {
  overflow: hidden;
  color: var(--ink);
  font-size: var(--fs-3);
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.item__dir {
  overflow: hidden;
  color: var(--ink-3);
  font-size: var(--fs-1);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.item__count {
  color: var(--ink-2);
  font-size: var(--fs-2);
  text-align: right;
  white-space: nowrap;
}

.item__status {
  min-width: 0;
}

.item__remove {
  justify-self: end;
  opacity: 0;
}

.item:hover .item__remove,
.item__remove:focus-visible {
  opacity: 1;
}

.item__reason {
  padding: 0 10px 8px 36px;
  color: var(--warn);
  font-size: var(--fs-2);
  line-height: 1.5;
}

.item[data-tone='danger'] .item__reason {
  color: var(--bad);
}

.item__rail {
  position: relative;
  height: 2px;
  margin: 0 10px 0 36px;
  overflow: hidden;
  border-radius: var(--r-pill);
  background: var(--line);
}

.item__rail span {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: var(--tone-line, var(--accent-vivid));
  transition: width var(--t-slow) var(--ease);
}

.item[data-tone='success'] .item__rail span {
  background: var(--ok);
}

.item[data-tone='warning'] .item__rail span {
  background: var(--warn);
}

.item[data-tone='danger'] .item__rail span {
  background: var(--bad);
}

.item__rail--wait span {
  width: 32%;
  animation: rail-wait 1.4s var(--ease) infinite;
}

@keyframes rail-wait {
  0% {
    transform: translateX(-110%);
  }

  100% {
    transform: translateX(320%);
  }
}

/* ---- 展开后的明细台账 ---- */

.details {
  margin: 2px 10px 12px 36px;
  padding: 12px 14px 13px;
  border: 1px solid var(--line);
  border-radius: var(--r-3);
  background: var(--panel-2);
  animation: details-in var(--t-base) var(--ease);
}

.details__stats {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 16px;
  padding-bottom: 11px;
  border-bottom: 1px solid var(--line-2);
}

.stat {
  display: inline-flex;
  align-items: baseline;
  gap: 5px;
}

.stat__n {
  color: var(--ink);
  font-size: var(--fs-4);
  font-weight: 600;
  line-height: 1;
}

.stat__n--bad {
  color: var(--bad);
}

.stat__k {
  color: var(--ink-3);
  font-size: var(--fs-1);
}

.items {
  display: flex;
  flex-direction: column;
}

/* 虚拟滚动：只有明细多到需要它时才出滚动条，几百行的文档照旧整段展开。 */
.items--virtual {
  overflow-y: auto;
  overscroll-behavior: contain;
}

.items__pad {
  flex: none;
  margin: 0;
  padding: 0;
  border: 0;
}

.items__row {
  display: grid;
  grid-template-columns: 46px 58px minmax(0, 1fr) 16px minmax(0, 1.05fr);
  align-items: center;
  column-gap: 8px;
  min-height: 30px;
  border-bottom: 1px solid var(--line);
}

.items__row:last-child {
  border-bottom: 0;
}

.items__line {
  color: var(--ink-3);
  font-size: var(--fs-1);
}

.items__status {
  color: var(--ink-2);
  font-size: var(--fs-1);
  font-weight: 500;
  white-space: nowrap;
}

.items__row[data-status='uploaded'] .items__status {
  color: var(--ok);
}

.items__row[data-status='reused'] .items__status {
  color: var(--accent-ink);
}

.items__row[data-status='missing'] .items__status {
  color: var(--warn);
}

.items__row[data-status='failed'] .items__status {
  color: var(--bad);
}

.items__target,
.items__result {
  overflow: hidden;
  font-size: var(--fs-1);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.items__target {
  color: var(--ink-2);
}

.items__result {
  color: var(--ink-3);
}

.items__row[data-status='uploaded'] .items__result,
.items__row[data-status='reused'] .items__result {
  color: var(--accent-ink);
}

.items__row[data-status='failed'] .items__result {
  color: var(--bad);
}

.items__arrow {
  display: grid;
  place-items: center;
  color: var(--line-3);
}

.items__arrow-icon {
  transform: rotate(-90deg);
}

.items__arrow--on {
  color: var(--accent);
}

.details__empty {
  padding: 10px 0 2px;
  color: var(--ink-3);
  font-size: var(--fs-2);
}

.details__out {
  display: flex;
  gap: 8px;
  margin-top: 10px;
  padding-top: 10px;
  border-top: 1px solid var(--line-2);
  color: var(--ink-2);
  font-size: var(--fs-1);
}

.details__out .mono {
  overflow: hidden;
  color: var(--ink-3);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.warnings {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-top: 8px;
  color: var(--warn);
  font-size: var(--fs-1);
  line-height: 1.5;
}

.warnings li::before {
  margin-right: 6px;
  content: '·';
}

@keyframes details-in {
  from {
    opacity: 0;
    transform: translateY(-3px);
  }

  to {
    opacity: 1;
    transform: none;
  }
}

@media (max-width: 820px) {
  .item__count {
    display: none;
  }

  .item__main {
    grid-template-columns: minmax(0, 1fr) 96px 34px;
  }

  .items__row {
    grid-template-columns: 46px 58px minmax(0, 1fr);
  }

  .items__arrow,
  .items__result {
    display: none;
  }
}
</style>
