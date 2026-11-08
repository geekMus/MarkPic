<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import AppIcon from './AppIcon.vue'
import StatusPill from './StatusPill.vue'
import type { FileStatus, ImageItem, ImageStatus } from '../types'
import type { RunRowView } from '../composables/useRun'

type Tone = 'neutral' | 'accent' | 'success' | 'warning' | 'danger'

const props = defineProps<{ row: RunRowView; busy: boolean }>()

const emit = defineEmits<{ remove: [id: string] }>()

const FILE_STATUS_META: Record<FileStatus, { tone: Tone; label: string }> = {
  ok: { tone: 'success', label: '完成' },
  partial: { tone: 'warning', label: '部分失败' },
  'no-images': { tone: 'neutral', label: '无图片' },
  failed: { tone: 'danger', label: '失败' },
  preview: { tone: 'neutral', label: '预览' },
}

const IMAGE_STATUS_META: Record<ImageStatus, { tone: Tone; label: string }> = {
  uploaded: { tone: 'success', label: '已上传' },
  reused: { tone: 'accent', label: '已复用' },
  remote: { tone: 'neutral', label: '远程链接' },
  missing: { tone: 'warning', label: '文件缺失' },
  failed: { tone: 'danger', label: '上传失败' },
  pending: { tone: 'neutral', label: '待上传' },
}

const expanded = ref(false)

const detailsId = `file-row-${useId()}`

const statusMeta = computed<{ tone: Tone; label: string }>(() => {
  const result = props.row.result
  if (result) return FILE_STATUS_META[result.status]
  if (props.row.phase === 'running') return { tone: 'accent', label: '上传中' }
  if (props.row.phase === 'cancelled') return { tone: 'warning', label: '已取消' }
  if (props.row.phase === 'settled') return { tone: 'neutral', label: '已结束' }
  if (props.row.missing) return { tone: 'neutral', label: '未找到文件' }
  return { tone: 'neutral', label: '待转换' }
})

const countLabel = computed(() => {
  const images = props.row.result?.images ?? props.row.images
  if (images === null || images === undefined) return props.row.missing ? '—' : '待统计'
  return `${images} 张图`
})

const reason = computed(() => {
  const result = props.row.result
  if (!result) {
    if (props.row.phase === 'cancelled') return '已取消，未处理的图片保持原样'
    if (props.row.missing) return '这个路径下没有 Markdown 文件'
    return ''
  }
  if (result.error) return result.error
  if (result.failed > 0) return `${result.failed} 张图片未处理`
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

const barVisible = computed(() => props.row.phase !== 'queued' || props.row.result !== null)

const items = computed<ImageItem[]>(() => props.row.result?.items ?? [])

/**
 * 同一张图在本轮已经上传过、这里直接复用地址的引用数。
 * 注意 uploaded 是「指向图床的引用数」（含复用），skipped 是「跳过的引用数」
 * （网络图片等），两者都不是上传请求数，所以分开显示。
 */
const reusedCount = computed(() => items.value.filter(item => item.status === 'reused').length)

function itemDetail(item: ImageItem): string {
  if (item.status === 'failed') return item.error ?? '上传失败'
  if (item.status === 'missing') return item.error ?? '本地文件不存在'
  if (item.status === 'remote') return '已是远程地址，跳过'
  if (item.status === 'pending') return '本地文件存在，尚未上传'
  return item.url ?? ''
}

/** 只有真正改写过的引用才配得上一条强调色的替换箭头。 */
function replaced(status: ImageStatus): boolean {
  return status === 'uploaded' || status === 'reused'
}
</script>

<template>
  <li class="row" :data-tone="statusMeta.tone">
    <div class="row__head">
      <button
        type="button"
        class="row__toggle"
        :aria-expanded="expanded"
        :aria-controls="detailsId"
        :title="expanded ? '收起图片明细' : '展开图片明细'"
        @click="expanded = !expanded"
      >
        <span class="row__chev" :class="{ 'row__chev--open': expanded }" aria-hidden="true">
          <AppIcon name="chevron-down" :size="14" />
        </span>

        <span class="row__id">
          <span class="row__name">{{ row.name }}</span>
          <span class="row__dir mono" :title="row.path">{{ row.dir }}</span>
        </span>
      </button>

      <span class="row__count num">{{ countLabel }}</span>

      <span class="row__status">
        <StatusPill :label="statusMeta.label" :tone="statusMeta.tone" compact />
      </span>

      <button
        v-if="!busy"
        type="button"
        class="icon-btn row__remove"
        :aria-label="`从队列移除 ${row.name}`"
        title="从队列移除"
        @click="emit('remove', row.id)"
      >
        <AppIcon name="trash" :size="15" />
      </button>
    </div>

    <p v-if="reason" class="row__reason">{{ reason }}</p>

    <div v-if="barVisible" class="row__rail" :class="{ 'row__rail--indeterminate': indeterminate }">
      <div v-if="!indeterminate" class="row__rail-fill" :style="{ width: `${percent}%` }" />
      <div v-else class="row__rail-slide" />
    </div>

    <div v-if="expanded" :id="detailsId" class="row__details">
      <template v-if="row.result">
        <p class="row__stats num">
          替换 {{ row.result.uploaded }} · 其中复用 {{ reusedCount }} · 跳过 {{ row.result.skipped }} ·
          失败 {{ row.result.failed }}
        </p>

        <ul v-if="items.length" class="items">
          <li v-for="(item, index) in items" :key="`${item.line}-${index}`" class="item">
            <span class="item__line num">L{{ item.line }}</span>
            <span class="item__status" :data-tone="IMAGE_STATUS_META[item.status].tone">
              {{ IMAGE_STATUS_META[item.status].label }}
            </span>
            <span class="item__target mono" :title="item.absolutePath">{{ item.target }}</span>
            <span
              class="item__arrow"
              :class="{ 'item__arrow--on': replaced(item.status) }"
              aria-hidden="true"
            >
              →
            </span>
            <span class="item__detail mono" :class="{ 'item__detail--muted': !replaced(item.status) }">
              {{ itemDetail(item) }}
            </span>
          </li>
        </ul>
        <p v-else class="row__note">该文件未引用本地图片</p>

        <p v-if="row.result.outputPath" class="row__note">
          输出 <span class="mono">{{ row.result.outputPath }}</span>
        </p>

        <ul v-if="row.result.warnings?.length" class="warnings">
          <li v-for="(warning, index) in row.result.warnings" :key="index" class="warning">
            <AppIcon name="alert" :size="13" />
            <span>{{ warning }}</span>
          </li>
        </ul>
      </template>

      <p v-else class="row__note">转换结束后显示图片明细</p>
    </div>
  </li>
</template>

<style scoped>
/* 台账的列宽必须与 FileQueue 的列头保持一致：
   文件 minmax(0,1fr) / 引用 64px / 状态 92px / 移除 30px */
.row {
  position: relative;
  border-bottom: 1px solid var(--line);
  background: var(--surface);
  --tone: var(--ink-3);
}

.row:last-child {
  border-bottom: none;
}

.row[data-tone='accent'] {
  --tone: var(--accent);
}

.row[data-tone='success'] {
  --tone: var(--ok);
}

.row[data-tone='warning'] {
  --tone: var(--warn);
}

.row[data-tone='danger'] {
  --tone: var(--bad);
}

/* 状态脊线：整份台账左边缘连续的一列颜色，扫一眼就知道哪几行没走通 */
.row::before {
  content: '';
  position: absolute;
  inset: 0 auto 0 0;
  width: 2px;
  background: var(--tone);
}

.row__head {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 64px 92px 30px;
  align-items: center;
  column-gap: 8px;
  padding-right: 6px;
}

.row__toggle {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  padding: 9px 0 9px 13px;
  border: none;
  background: transparent;
  color: inherit;
  font: inherit;
  text-align: left;
  cursor: pointer;
  transition: background-color var(--t-fast) var(--ease);
}

.row__toggle:hover {
  background: var(--sunk);
}

.row__chev {
  display: grid;
  place-items: center;
  width: 16px;
  height: 16px;
  flex: none;
  color: var(--ink-3);
  transition:
    transform var(--t-fast) var(--ease),
    color var(--t-fast) var(--ease);
}

.row__chev--open {
  transform: rotate(180deg);
  color: var(--ink-2);
}

.row__id {
  display: flex;
  flex-direction: column;
  gap: 2px;
  flex: 1;
  min-width: 0;
}

.row__name {
  color: var(--ink);
  font-size: var(--fs-body);
  font-weight: 550;
  line-height: 1.35;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.row__dir {
  color: var(--ink-3);
  font-size: var(--fs-label);
  line-height: 1.35;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.row__count {
  color: var(--ink-3);
  font-size: var(--fs-label);
  text-align: right;
  white-space: nowrap;
}

.row__status {
  display: flex;
  justify-content: flex-end;
}

.row__remove {
  opacity: 0;
  transition: opacity var(--t-fast) var(--ease);
}

.row:hover .row__remove,
.row__remove:focus-visible {
  opacity: 1;
}

.row__reason {
  padding: 0 13px 8px 39px;
  color: var(--tone);
  font-size: var(--fs-label);
  line-height: 1.5;
  overflow-wrap: anywhere;
}

.row__rail {
  position: relative;
  height: 2px;
  overflow: hidden;
  background: var(--track);
}

.row__rail-fill {
  height: 100%;
  background: var(--tone);
  transition: width var(--t-slow) var(--ease);
}

.row__rail-slide {
  width: 30%;
  height: 100%;
  background: var(--tone);
  animation: rail-slide 1.1s var(--ease) infinite;
}

.row__details {
  padding: 10px 13px 12px 39px;
  border-top: 1px solid var(--line);
  background: var(--sunk);
  animation: details-in var(--t-base) var(--ease);
}

.row__stats {
  color: var(--ink-2);
  font-size: var(--fs-label);
}

.row__note {
  margin-top: 6px;
  color: var(--ink-3);
  font-size: var(--fs-label);
  line-height: 1.6;
  overflow-wrap: anywhere;
}

.items {
  display: flex;
  flex-direction: column;
  margin-top: 8px;
  border: 1px solid var(--line);
  border-radius: var(--r-2);
  background: var(--surface);
  overflow: hidden;
}

.item {
  display: grid;
  grid-template-columns: 34px 52px minmax(0, 1fr) 12px minmax(0, 1.1fr);
  align-items: baseline;
  gap: 6px;
  padding: 6px 9px;
  border-bottom: 1px solid var(--line);
  font-size: var(--fs-label);
}

.item:last-child {
  border-bottom: none;
}

.item__line {
  color: var(--ink-3);
  font-size: 10.5px;
}

.item__status {
  color: var(--ink-2);
  white-space: nowrap;
}

.item__status[data-tone='success'] {
  color: var(--ok);
}

.item__status[data-tone='accent'] {
  color: var(--accent-ink);
}

.item__status[data-tone='warning'] {
  color: var(--warn);
}

.item__status[data-tone='danger'] {
  color: var(--bad);
}

.item__target,
.item__detail {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 不使用 direction: rtl 做「左侧省略」：bidi 重排会把开头的 ./ 或盘符挪到末尾，
   反而读不出路径。这里正常书写方向截断，完整路径由 title 提供。 */
.item__target {
  color: var(--ink-2);
}

.item__detail {
  color: var(--ink-2);
}

.item__detail--muted {
  color: var(--ink-3);
}

.item__arrow {
  color: var(--line-2);
  text-align: center;
}

.item__arrow--on {
  color: var(--accent);
}

.warnings {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-top: 7px;
}

.warning {
  display: flex;
  align-items: flex-start;
  gap: 5px;
  color: var(--warn);
  font-size: var(--fs-label);
  line-height: 1.55;
  overflow-wrap: anywhere;
}

@keyframes rail-slide {
  from {
    transform: translateX(-100%);
  }
  to {
    transform: translateX(340%);
  }
}

@keyframes details-in {
  from {
    opacity: 0;
  }
  to {
    opacity: 1;
  }
}
</style>
