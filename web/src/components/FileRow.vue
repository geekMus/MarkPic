<script setup lang="ts">
import { computed, ref, useId } from 'vue'
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
}

const expanded = ref(false)

const detailsId = `file-row-${useId()}`

const statusMeta = computed<{ tone: Tone; labelKey: MessageKey }>(() => {
  const result = props.row.result
  if (result) return FILE_STATUS_META[result.status]
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
  if (result.failed > 0) return t('row.reason.failed', { count: result.failed })
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

/** 后端把 pending 也算进 skipped（那是给命令行汇总用的），展示时要把它们剥离出来。 */
const skippedOnly = computed(() => Math.max(0, (props.row.result?.skipped ?? 0) - pendingCount.value))

/**
 * 展开后没有明细可列时说的话，要跟这一行的实际处境对得上。
 * （路径根本没匹配到文件的行不展开，见 noDetails，所以这里只剩「没有本地图片」一种情况。）
 */
const emptyDetailsText = computed(() => t('row.details.empty'))

/** 没有逐图明细的行（例如路径下压根没有 Markdown）就不该给一个能点的展开箭头。 */
const noDetails = computed(() => Boolean(props.row.missing) && !props.row.result)

function itemDetail(item: ImageItem): string {
  if (item.status === 'failed') return item.error ?? t('row.status.uploadFailed')
  if (item.status === 'missing') return item.error ?? t('row.item.missingFile')
  if (item.status === 'remote') return t('row.item.remoteSkipped')
  // 待上传/已取消的引用：状态列已经写了，这里再重复一句就是噪音
  if (item.status === 'pending' || item.status === 'cancelled') return ''
  return item.url ?? ''
}

/** 只有真正改写过的引用才配得上一条强调色的替换箭头。 */
function replaced(status: ImageStatus): boolean {
  return status === 'uploaded' || status === 'reused' || status === 'cached'
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
        @click="expanded = !expanded"
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
        <span v-if="(row.result?.uploaded ?? 0) > 0 || pendingCount === 0" class="stat">
          <span class="stat__n num">{{ row.result?.uploaded ?? 0 }}</span>
          <span class="stat__k">{{ t('row.stat.uploaded') }}</span>
        </span>
        <span v-if="reusedCount > 0" class="stat">
          <span class="stat__n num">{{ reusedCount }}</span>
          <span class="stat__k">{{ t('row.stat.reused') }}</span>
        </span>
        <span v-if="pendingCount > 0" class="stat">
          <span class="stat__n num">{{ pendingCount }}</span>
          <span class="stat__k">{{ t('row.stat.pending') }}</span>
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

      <ul v-if="items.length > 0" class="items">
        <li v-for="(item, index) in items" :key="index" class="items__row" :data-status="item.status">
          <span class="items__line mono">L{{ item.line }}</span>
          <span class="items__status">{{ t(IMAGE_STATUS_META[item.status].labelKey) }}</span>
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
