<script setup lang="ts">
import { computed, nextTick, ref, useId, watch } from 'vue'
import AppIcon from './AppIcon.vue'
import StatusPill from './StatusPill.vue'
import type { FileResult, FileStatus, ImageItem, ImageStatus } from '../types'
import type { RunRowView } from '../composables/useRun'
import { useI18n, type MessageKey } from '../i18n'
import { redactUrl } from '../composables/useDeadLinks'
import { describeError, openTarget } from '../api'
import { useToasts } from '../composables/useToasts'

type Tone = 'neutral' | 'accent' | 'success' | 'warning' | 'danger'

const props = defineProps<{
  row: RunRowView
  busy: boolean
  /**
   * 这一轮在做什么。只影响「跑起来时那一瞬间」的说法：上传模式下写「上传中」，
   * 下载模式写「下载中」，检测模式写「检测中」。这三个词本来就各不相同，
   * 一律叫「上传中」会让只检测/只下载的人以为程序在做别的事。
   */
  remoteMode?: 'ignore' | 'check' | 'download'
  /** 这一轮是不是被提前掐断的（图床连不上）。只影响「取消」那行的说法。 */
  aborted?: boolean
  /** 右栏是不是正在看这一行。 */
  selected?: boolean
}>()

const emit = defineEmits<{ remove: [id: string]; reveal: [path: string]; select: [id: string] }>()

const { danger } = useToasts()

/**
 * 这一行刚跑完：底色亮一下再褪掉。
 * 状态从「检测中」直接变成「已下载」，如果什么动静都没有，
 * 人会觉得它没跑（尤其批量跑的时候）。只闪一次，900ms。
 */
const settled = ref(false)

watch(
  () => props.row.phase,
  (next) => {
    if (next !== 'settled') return
    settled.value = true
    window.setTimeout(() => {
      settled.value = false
    }, 900)
  },
)

/** 打开这次写出来的那份文件；失败就把原因说出来，别让按钮像没反应。 */
async function openOutput(): Promise<void> {
  const target = props.row.result?.outputPath ?? ''
  if (target === '') return
  try {
    await openTarget(target)
  } catch (error) {
    danger(describeError(error, t('error.openOutput')))
  }
}

/** 已跑完的行给的是副本，其它情况给原文档 —— 想干活的总是这两者之一。 */
const revealTarget = computed<string>(() => {
  const out = props.row.result?.outputPath ?? ''
  return out !== '' ? out : props.row.path
})

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

/** 跑起来的那一瞬间该说什么，取决于这一轮在做什么。 */
const runningLabelKey = computed<MessageKey>(() => {
  switch (props.remoteMode) {
    case 'download':
      return 'row.status.downloading'
    case 'check':
      return 'row.status.checking'
    default:
      return 'row.status.uploading'
  }
})

/** 还没开跑那一格也一样：检测视图里写着「待转换」等于让人以为菜单点错了。 */
const queuedLabelKey = computed<MessageKey>(() => {
  switch (props.remoteMode) {
    case 'download':
      return 'row.status.queued.download'
    case 'check':
      return 'row.status.queued.check'
    default:
      return 'row.status.queued.convert'
  }
})

const statusMeta = computed<{ tone: Tone; labelKey: MessageKey }>(() => {  const result = props.row.result
  if (result) {
    const meta = FILE_STATUS_META[result.status]
    // 检测模式下整篇「已检测」读起来是成功，但有死链的行必须一眼认出来 ——
    // 尤其点开「只看有问题的」之后，一行行全是中性的「已检测」等于没筛。
    //
    // 这里只改颜色、不改说法。早先死链行一律换标题成「已检测」，于是下载模式
    // 下那些「下载了一半」的行也写着「已检测」—— 看着像只查了查、没动过文件。
    if ((result.dead ?? 0) > 0 || (result.remoteFailed ?? 0) > 0) {
      return { tone: 'danger', labelKey: meta.labelKey }
    }
    // 缺本地图的行只算警告（不红），但它确实有问题，筛选得能看见。
    if ((result.localMissing ?? 0) > 0) {
      return { tone: 'warning', labelKey: meta.labelKey }
    }
    // 图床连不上被掐断时，这些行其实一张都没轮到 —— 写「已取消」会让人以为
    // 是自己点错了。只有真按下取消时才说「已取消」。
    if (result.status === 'cancelled' && props.aborted) {
      return { tone: 'warning', labelKey: 'row.status.aborted' }
    }
    return meta
  }
  if (props.row.phase === 'running') return { tone: 'accent', labelKey: runningLabelKey.value }
  if (props.row.phase === 'cancelled') return { tone: 'warning', labelKey: 'row.status.cancelled' }
  if (props.row.phase === 'settled') return { tone: 'neutral', labelKey: 'row.status.finished' }
  if (props.row.missing) return { tone: 'neutral', labelKey: 'row.status.notFound' }
  return { tone: 'neutral', labelKey: queuedLabelKey.value }
})

const countLabel = computed(() => {
  const images = props.row.result?.images ?? props.row.images
  if (images === null || images === undefined) {
    return props.row.missing ? '—' : t('row.count.pending')
  }
  return t('row.count', { count: images })
})

/**
 * 卡面只写最后一级目录名：整条 `D:\Projects\convert-md-picture\.tmp-scroll`
 * 在每张卡上都重复一遍，十张卡就是十行一样的灰字。完整路径放在 title 里。
 */
const shortDir = computed(() => {
  const parts = props.row.dir.split(/[\\/]/).filter((part) => part !== '')
  return parts.length > 0 ? parts[parts.length - 1] : props.row.dir
})

/**
 * 展开后的第一句：这一篇里的图到底怎么样。
 *
 * 早先这里是「预览 · 1 张图片未处理」——「预览」不是结论，「未处理」也不是
 * 事实（预检根本没试过），两句话加起来什么也没说。现在按模式直接讲清楚：
 * 扫到几张、换掉几处、几个地址能打开、几张存回了本地。
 */
const lede = computed(() => {
  const result = props.row.result
  const mode = props.remoteMode ?? 'ignore'
  if (!result) {
    if (props.row.phase === 'cancelled') return t('row.reason.cancelled')
    if (props.row.missing) return t('row.reason.noFiles')
    if (props.row.phase === 'running') return t('row.lede.running')
    return t('row.lede.waiting')
  }
  // 整篇失败时，原因比数目要紧：「连不上 PicGo」比「3 张没成功」有用。
  if (result.error) {
    const nothingDone = (result.uploaded ?? 0) === 0 && (result.localized ?? 0) === 0
    if (nothingDone) {
      const first = items.value.find((item) => item.status === 'failed' && item.error)
      if (first?.error) return first.error
    }
    return result.error
  }

  const total = items.value.length
  const bad = items.value.filter((item) => item.status === 'remote-bad').length
  const parts: (() => string)[] = []

  if (mode === 'check') {
    if (total === 0) return t('row.lede.noImages')
    parts.push(() => t('row.lede.check', { total, ok: total - bad - remoteUnsupportedCount.value, bad }))
  } else if (mode === 'download') {
    if (total === 0) return t('row.lede.noImages')
    parts.push(() => t('row.lede.download', { total, saved: localizedUniqueCount.value }))
    if (localKeptCount.value > 0) parts.push(() => t('row.lede.kept', { count: localKeptCount.value }))
  } else if (result.status === 'preview') {
    // 预检只是扫了一遍：没有「替换」也没有「失败」，只有「扫到什么」。
    if (total === 0) return t('row.lede.noImages')
    // 试运行跑完还是「预览」态（什么都没写），得把「跑过了、只是没动文件」说出来，
    // 否则点完「开始上传」这一页一个字都不变，像没反应。
    if (props.row.phase === 'settled') {
      return t('row.lede.dryRun', { count: total })
    }
    parts.push(() => t('row.lede.scanLocal', { count: total }))
    if (missingCount.value > 0) parts.push(() => t('row.lede.missing', { count: missingCount.value }))
  } else {
    if ((result.uploaded ?? 0) > 0) parts.push(() => t('row.lede.uploaded', { count: result.uploaded ?? 0 }))
    if (reusedCount.value > 0) parts.push(() => t('row.lede.reused', { count: reusedCount.value }))
    if (localKeptCount.value > 0) parts.push(() => t('row.lede.kept', { count: localKeptCount.value }))
  }

  if (skippedOnly.value > 0) parts.push(() => t('row.lede.skipped', { count: skippedOnly.value }))
  if (failureCount.value > 0) parts.push(() => t('row.lede.failed', { count: failureCount.value }))
  if (mode !== 'check') {
    const dead = deadCount(result)
    if (dead > 0) parts.push(() => t('row.lede.dead', { count: dead }))
    if (remoteUnsupportedCount.value > 0) {
      parts.push(() => t('row.lede.unsupported', { count: remoteUnsupportedCount.value }))
    }
  }

  if (parts.length === 0) return result.warnings?.[0] ?? t('row.lede.noImages')
  return parts.map((part) => part()).join(t('row.lede.join'))
})

const percent = computed(() => {
  if (props.row.phase === 'settled' || props.row.phase === 'cancelled') return 100
  if (props.row.total > 0) {
    // 远程模式只动 done（这一篇处理了几张），上传模式只动 uploaded（替换了几处）。
    const moved = props.row.done ?? props.row.uploaded
    return Math.min(100, Math.round((moved / props.row.total) * 100))
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
// 只在真跑的时候画进度条：排队中的卡片如果也挂一条闪动的线，八张卡会一起闪，像一堆进度条
const barVisible = computed(() => props.row.phase === 'running')

const items = computed<ImageItem[]>(() => props.row.result?.items ?? [])

/**
 * 同一张图在本轮已经上传过、这里直接复用地址的引用数。
 * 注意 uploaded 是「指向图床的引用数」（含复用），skipped 是「跳过的引用数」
 * （网络图片等），两者都不是上传请求数，所以分开显示。
 */
const reusedCount = computed(
  () => items.value.filter(item => item.status === 'reused' || item.status === 'cached').length,
)

/** 引用的本地文件根本不在：预检就能看出来，不试上传。 */
const missingCount = computed(() => items.value.filter(item => item.status === 'missing').length)

/** 真试过、真没成的那几张（本地文件不存在不算，那是另一个问题）。 */
const failureCount = computed(() => items.value.filter(item => item.status === 'failed').length)

/** 预检模式下所有待上传的引用都还没有真正上传，单独数一遍，别混进「跳过」。 */
const pendingCount = computed(() => items.value.filter(item => item.status === 'pending').length)

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
/**
 * 这一行里「有几个坏掉的地址」。
 *
 * 同一个死链被引用 3 次时 dead 是 3、deadUnique 是 1：行里说「3 个地址打不开」
 * 而清单里只有 1 个，读的人会以为坏了三张图。下载模式还要把「这次没下来」的
 * 一起算上 —— 对用户来说那是同一件事：「这张图没拿到」。
 */
function deadCount(result: FileResult): number {
  const dead = result.dead ?? 0
  const failed = result.remoteFailed ?? 0
  const total = dead + failed
  const unique = (result.deadUnique ?? 0) + (result.remoteFailedUnique ?? 0)
  return unique > 0 && unique < total ? unique : total
}

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
/**
 * 展开后没有明细可列时说的话，要跟这一行的实际处境对得上。
 * （路径根本没匹配到文件的行不展开，见 canExpand，所以这里只剩「没有本地图片」一种情况。）
 */
const emptyDetailsText = computed(() => t('row.details.empty'))

/** 预检的时候文件还没写出去，说「输出」会让人以为已经生成好了。 */
const outputLabel = computed<MessageKey>(() =>
  props.row.result?.status === 'preview' ? 'row.output.planned' : 'row.output',
)

/** 有逐图明细才能展开：判据直接看结果里的 items，不再绕「缺文件」那一层推断。 */
const canExpand = computed(() => (props.row.result?.items?.length ?? 0) > 0)

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
  // 下载模式下「沿用上次」（上次就下好了，这一轮没再碰网络）与「这次下的」
  // 也是两件事：合并成一个标签，用户会以为这一轮又走了一遍网络。
  if (item.status === 'localized' && item.reused) {
    return t('row.status.localReused')
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
  // 检测模式下，这一列的「结果」是结论，不是把地址再抄一遍（地址就在左边）
  if (item.status === 'remote-ok') return t('row.item.remoteOk')
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

/** 超过这个条数就收进定高的小窗口：一百张图的文档不能让卡片撑成三千像素。 */
const LIST_LIMIT = 12
/** 超过这个条数才启用虚拟滚动：几十行全渲染反而更简单，也不会有滚动条。 */
const VIRTUAL_THRESHOLD = 60
/** 明细行高度（`.items__row` 的 min-height 30px，单行不换行）。 */
const ITEM_HEIGHT = 30
/** 可视区上下各多渲染几行，滚动时不至于露白。 */
const ITEM_OVERSCAN = 8
/** 虚拟滚动时明细区的最大高度：再高就把整页滚动条抢走了。 */
const ITEM_VIEWPORT = 420

const itemsEl = ref<HTMLElement | null>(null)
const itemsScrollTop = ref(0)
const itemsViewport = ref(ITEM_VIEWPORT)

const bounded = computed(() => items.value.length > LIST_LIMIT)
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

/** 点行就是「选中它」：右栏跟着换内容；有明细的行顺带展开，保持原来的手感。 */
function onRowClick(): void {
  emit('select', props.row.id)
  if (canExpand.value) toggleExpanded()
}

/* 展开/收起的高度动画：先量出自然高度，再在 0 与它之间过渡。
   没有这层，点「查看详情」是一瞬间蹦出一大块，读起来很生硬。 */
function onAccEnter(el: Element): void {
  const node = el as HTMLElement
  const height = node.scrollHeight
  node.style.height = '0px'
  node.style.opacity = '0'
  requestAnimationFrame(() => {
    node.style.height = `${height}px`
    node.style.opacity = '1'
  })
}

function onAccLeave(el: Element): void {
  const node = el as HTMLElement
  node.style.height = `${node.scrollHeight}px`
  node.style.opacity = '1'
  requestAnimationFrame(() => {
    node.style.height = '0px'
    node.style.opacity = '0'
  })
}

function onAccDone(el: Element): void {
  const node = el as HTMLElement
  node.style.height = ''
  node.style.opacity = ''
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

</script>

<template>
  <li
    class="item"
    :class="{ 'item--settled': settled }"
    :data-tone="statusMeta.tone"
    :data-selected="props.selected ? 'yes' : null"
  >
    <div class="card">
      <button
        type="button"
        class="card__head"
        :disabled="!canExpand"
        :aria-expanded="canExpand ? expanded : undefined"
        :aria-controls="canExpand ? detailsId : undefined"
        :title="!canExpand ? row.path : expanded ? t('row.details.collapse') : t('row.details.expand')"
        @click="onRowClick"
      >
        <span class="item__mark" aria-hidden="true">
          <!-- 一枚小方块垫在名字左边：一列卡片里每张都有个落点，
               状态靠它的底色说话，不靠一行字。 -->
          <AppIcon :name="row.isDir ? 'folder' : 'doc'" :size="16" />
        </span>

        <span class="item__id">
          <span class="item__name">{{ row.name }}</span>
          <span class="item__dir mono" :title="row.path">{{ shortDir }}</span>
        </span>

        <span class="item__count">
          <span class="item__dot" aria-hidden="true" />
          <span class="num">{{ countLabel }}</span>
        </span>

        <!-- 没有明细可看的时候，状态只能写在卡面上；有明细就把这句话收进「查看详情」里 -->
        <span v-if="!canExpand" class="item__status">
          <StatusPill :label="t(statusMeta.labelKey)" :tone="statusMeta.tone" compact />
        </span>
        <span v-else class="item__more">
          {{ t(expanded ? 'row.details.collapse' : 'row.details.expand') }}
          <span class="item__chev" :class="{ 'item__chev--on': expanded }" aria-hidden="true">
            <AppIcon name="chevron-down" :size="13" />
          </span>
        </span>
      </button>

      <div v-if="barVisible" class="item__rail" :class="{ 'item__rail--wait': indeterminate }">
        <span :style="indeterminate ? undefined : { width: percent + '%' }" />
      </div>

      <Transition
        name="acc"
        @enter="onAccEnter"
        @after-enter="onAccDone"
        @leave="onAccLeave"
        @after-leave="onAccDone"
      >
      <div v-if="expanded && canExpand" :id="detailsId" class="details">
        <div class="details__head">
          <p class="details__lede" :title="lede">{{ lede }}</p>
          <span class="details__ops">
            <button
              type="button"
              class="icon-btn icon-btn--sm"
              :title="t('row.reveal')"
              :aria-label="t('row.reveal')"
              @click.stop="emit('reveal', revealTarget)"
            >
              <AppIcon name="folder-open" :size="14" />
            </button>
            <button
              type="button"
              class="icon-btn icon-btn--sm icon-btn--danger"
              :title="t('row.remove')"
              :aria-label="t('row.remove')"
              :disabled="busy"
              @click.stop="emit('remove', row.id)"
            >
              <AppIcon name="x" :size="13" />
            </button>
          </span>
        </div>
      <ul
        v-if="items.length > 0"
        ref="itemsEl"
        class="items"
        :class="{ 'items--scroll': bounded }"
        :style="bounded ? { maxHeight: ITEM_VIEWPORT + 'px' } : undefined"
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
          <span class="items__target mono" :title="redactUrl(item.absolutePath || item.target)">
            {{ redactUrl(item.target) }}
          </span>
          <span class="items__result" :title="itemDetail(item) || itemLabel(item)">
            {{ itemDetail(item) || itemLabel(item) }}
          </span>
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
        <span>{{ t(outputLabel) }}</span>
        <span class="mono">{{ row.result.outputPath }}</span>
        <button type="button" class="btn btn--ghost btn--sm" @click="openOutput">
          {{ t('common.open') }}
        </button>
      </p>

      <ul v-if="row.result?.warnings?.length" class="warnings">
        <li v-for="(warning, index) in row.result.warnings" :key="index">{{ warning }}</li>
      </ul>
      </div>
      </Transition>
    </div>
  </li>
</template>

<style scoped>
/* 一张卡 = 一个文件。卡与卡之间靠 .item 自己的下边距隔开 ——
   滚动高度是按行元素量的，用 flex 的 gap 会让每一行少算 10px。 */
.item {
  position: relative;
  padding: 0 0 10px;
  list-style: none;
}

.card {
  position: relative;
  overflow: hidden;
  border: 1px solid var(--card-edge);
  border-radius: var(--r-4);
  /* 顶亮底暗的一层极淡渐变：卡片在两种主题下都有一点「面」的厚度，不是一块色板 */
  background: linear-gradient(180deg, var(--card-top), var(--card-bottom));
  box-shadow: var(--shadow-card);
  transition:
    border-color 160ms var(--ease-out),
    box-shadow 160ms var(--ease-out),
    background 160ms var(--ease-out);
}

.item:hover .card {
  border-color: var(--line-2);
  box-shadow: var(--shadow-card-hover);
}

.item:active .card {
  box-shadow: var(--shadow-card);
}

.item--open .card {
  border-color: var(--line-2);
  box-shadow: var(--shadow-card-hover);
}

.item[data-selected='yes'] .card {
  border-color: var(--accent-line);
  background: color-mix(in srgb, var(--accent) 6%, var(--panel-2));
}

.card__head {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  padding: 13px 16px;
  border: 0;
  background: none;
  color: inherit;
  font: inherit;
  text-align: left;
  cursor: pointer;
}

.card__head:disabled {
  cursor: default;
}

/* 卡片左边的文件片：中性状态是一块「更暗的玻璃」，跑起来 / 成功 / 失败
   跟着状态换色。卡片本身已经有一层底色，这里用面板色垫出一点层次。 */
.item__mark {
  display: grid;
  flex: none;
  place-items: center;
  width: 36px;
  height: 36px;
  border: 1px solid var(--line);
  border-radius: var(--r-3);
  background: var(--panel);
  color: var(--ink-3);
  transition: color var(--t-fast) var(--ease), background var(--t-fast) var(--ease),
    border-color var(--t-fast) var(--ease);
}

.item[data-tone='accent'] .item__mark {
  border-color: var(--accent-line);
  background: var(--accent-soft);
  color: var(--accent);
}

.item[data-tone='success'] .item__mark {
  border-color: var(--ok-line);
  background: var(--ok-soft);
  color: var(--ok);
}

.item[data-tone='warning'] .item__mark {
  border-color: var(--warn-line);
  background: var(--warn-soft);
  color: var(--warn);
}

.item[data-tone='danger'] .item__mark {
  border-color: var(--bad-line);
  background: var(--bad-soft);
  color: var(--bad);
}

.item__id {
  display: flex;
  flex: 1 1 auto;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.item__name {
  min-width: 0;
  overflow: hidden;
  color: var(--ink);
  font-size: var(--fs-3);
  font-weight: 600;
  letter-spacing: -0.01em;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.item__dir {
  overflow: hidden;
  color: var(--ink-3);
  font-size: var(--fs-2);
  line-height: 1.45;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 图片数做成一枚小胶囊：纯数字在卡片右边太轻，立不住 */
.item__count {
  display: inline-flex;
  flex: none;
  align-items: center;
  gap: 6px;
  padding: 2px 8px;
  border: 1px solid var(--line);
  border-radius: var(--r-pill);
  background: var(--panel);
  color: var(--ink-2);
  font-size: var(--fs-1);
  line-height: 1.5;
  white-space: nowrap;
}

/* 状态收成一颗 6px 的点：颜色说话，不用「未处理」这种半截话占位 */
.item__dot {
  width: 6px;
  height: 6px;
  border-radius: var(--r-pill);
  background: var(--ink-3);
}

.item[data-tone='accent'] .item__dot {
  background: var(--accent);
}

.item[data-tone='success'] .item__dot {
  background: var(--ok);
}

.item[data-tone='warning'] .item__dot {
  background: var(--warn);
}

.item[data-tone='danger'] .item__dot {
  background: var(--bad);
}

.item__status {
  display: flex;
  flex: none;
}

/* 「查看详情」本身就是一个动作词，比「未处理」这种半截状态诚实 */
.item:hover .item__more {
  color: var(--ink-2);
}

.item__more {
  display: inline-flex;
  flex: none;
  align-items: center;
  gap: 6px;
  transition: color 140ms var(--ease-out);
  color: var(--ink-3);
  font-size: var(--fs-2);
  white-space: nowrap;
  transition: color var(--t-fast) var(--ease);
}

.card__head:hover .item__more,
.card__head:focus-visible .item__more {
  color: var(--ink-2);
}

.item__chev {
  display: inline-grid;
  place-items: center;
  width: 16px;
  height: 16px;
  transition: transform var(--t-base) var(--ease);
}

.item__chev--on {
  transform: rotate(180deg);
}

.item__rail {
  position: relative;
  height: 2px;
  margin: 0 14px 10px;
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

/* 行跑完闪一下：卡片描边换成强调色再褪回去，比整块底色跳一下安静 */
.item--settled .card {
  animation: card-settled 900ms var(--ease) both;
}

@keyframes card-settled {
  0% {
    border-color: var(--accent-line);
    background: color-mix(in srgb, var(--accent) 9%, var(--panel-2));
  }

  100% {
    border-color: var(--line);
    background: var(--panel-2);
  }
}

/* ---- 展开后的明细台账 ---- */

.details {
  margin: 0;
  padding: 12px 14px 14px;
  border-top: 1px solid var(--line);
  /* 高度由展开动画写在行内；这里只负责把高度与透明度的变化放顺 */
  overflow: hidden;
  transition:
    height 240ms var(--ease-out),
    opacity 180ms var(--ease),
    padding 240ms var(--ease-out);
}

/* 明细的第一行：状态本身就是一句结论，下面才是逐图台账 */
.details__head {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 10px;
}

.details__lede {
  flex: 1 1 auto;
  min-width: 0;
  margin: 0;
  color: var(--ink-2);
  font-size: var(--fs-2);
  line-height: 1.55;
}

.item[data-tone='danger'] .details__lede {
  color: var(--bad);
}

.item[data-tone='warning'] .details__lede {
  color: var(--warn);
}

.details__ops {
  display: flex;
  flex: none;
  gap: 2px;
  margin-left: auto;
}

.items {
  display: flex;
  flex-direction: column;
}

/* 明细长了就给一个定高窗口：卡片高度不随图片数增长，滚动留在这一块里。 */
.items--scroll {
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
  grid-template-columns: 40px minmax(0, 1.15fr) minmax(0, 1fr);
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
.items__row[data-status='reused'] .items__result,
.items__row[data-status='localized'] .items__result {
  color: var(--accent-ink);
}

.items__row[data-status='remote-ok'] .items__result {
  color: var(--ok);
}

.items__row[data-status='missing'] .items__result {
  color: var(--warn);
}

.items__row[data-status='failed'] .items__result,
.items__row[data-status='remote-bad'] .items__result {
  color: var(--bad);
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
  align-items: center;
  color: var(--ink-2);
  font-size: var(--fs-1);
}

.details__out .btn {
  flex: none;
  margin-left: auto;
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
    grid-template-columns: minmax(0, 1fr) 104px 56px;
  }

  .items__row {
    grid-template-columns: 40px minmax(0, 1fr);
  }

  .items__result {
    display: none;
  }
}
</style>
