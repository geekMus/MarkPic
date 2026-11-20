<script setup lang="ts">
import { computed } from 'vue'
import ReportShell from './ReportShell.vue'
import ResultGroup from './ResultGroup.vue'
import StatusPill from './StatusPill.vue'
import type { ImageItem } from '../types'
import type { RunRowView } from '../composables/useRun'
import { plural, useI18n } from '../i18n'
import type { MessageKey } from '../i18n'
import { shortReason } from '../composables/useDeadLinks'
import { useToasts } from '../composables/useToasts'
import { describeError, openTarget } from '../api'
import type { Tone } from './rowMeta'
import { isRemoteRef } from './target'
import { useCopyAddress } from './useCopyAddress'

/**
 * 下载页：这一页要回答的是「哪张远程图落到本地哪个文件了」。
 *
 * 所以正文不是文件表，而是一张「远程地址 → 本地文件」的映射：
 * 一个文档一段，每行一张远程图。跑之前本地那一栏写「待定」——
 * 名字要等真正落盘时才知道（重名会被改成 pic-2.png，提前猜就会骗人）。
 */
const props = defineProps<{
  rows: RunRowView[]
  recent?: string[]
  busy?: boolean
  progress?: number
}>()

const emit = defineEmits<{
  'pick-files': []
  'pick-folder': []
  'pick-recent': [path: string]
  remove: [id: string]
  reveal: [path: string]
  clear: []
  /** 卡片自己的扫描：取消这一份 / 恢复（重试）。 */
  'cancel-scan': [id: string]
  'retry-scan': [id: string]
}>()

const { t } = useI18n()
const { danger } = useToasts()
const { copyAddress } = useCopyAddress()

/** 卡头那几颗按钮指向的文件：这一轮写过副本就指副本，没跑过就指原文。 */
function fileOf(row: RunRowView): string {
  return row.result?.outputPath || row.path
}

/** 打开这份文件；失败要说原因，别让按钮像没反应。 */
async function openFile(row: RunRowView): Promise<void> {
  const target = fileOf(row)
  if (target === '') return
  try {
    await openTarget(target)
  } catch (error) {
    danger(describeError(error, t('error.openOutput')))
  }
}

/** 还没扫过张数时先别下结论：显示「正在扫」比显示「没有可下载的图」诚实。 */
const scanning = computed(() => props.rows.some((row) => (row.scan ?? 'idle') === 'scanning'))
const scanCount = computed(() => props.rows.filter((row) => (row.scan ?? 'idle') === 'scanning').length)

/** 进场错开最多 8 行：再多就一起出现，别让「慢慢浮上来」变成等待。 */

type Kind = 'saved' | 'reused' | 'failed' | 'unsupported' | 'pending'

const KIND_META: Record<Kind, { labelKey: MessageKey; tone: Tone }> = {
  saved: { labelKey: 'row.status.localized', tone: 'success' },
  reused: { labelKey: 'row.status.localReused', tone: 'accent' },
  failed: { labelKey: 'row.status.downloadFailed', tone: 'danger' },
  unsupported: { labelKey: 'row.status.remoteUnsupported', tone: 'warning' },
  pending: { labelKey: 'download.row.pending', tone: 'neutral' },
}

/**
 * ftp://、data: 这类地址不是「还没取回」，是根本取不回来。
 * 预览阶段后端不做网络动作，也就不会给它们打结论 —— 这里按地址本身先判掉，
 * 免得下载页把「永远下不了」写成「待定」。
 */
function unsupportedTarget(target: string): boolean {
  if (/^https?:/i.test(target)) return false
  return /^[a-z][a-z0-9+.-]*:/i.test(target) && !/^[a-z]:[\\/]/i.test(target)
}

function kindOf(item: ImageItem): Kind {
  if (unsupportedTarget(item.target)) return 'unsupported'
  if (item.status === 'localized') return item.reused ? 'reused' : 'saved'
  // 下载回来失败（404、连不上、不是图片）在结果里是 remote-bad，不是 failed：
  // failed 留给「写文档/落盘」这类本地失败。
  if (item.status === 'failed' || item.status === 'remote-bad') return 'failed'
  // 预览阶段远程图一律是「原样保留」（status=remote、没有 error）：那是还没取回，
  // 不是没法处理。真跑完之后才会变成 localized / failed。
  if (item.status === 'remote' && item.error) return 'unsupported'
  return 'pending'
}

/** 落盘位置在结果里是绝对路径；页面上只写相对文档的那一段（images\a.webp）。 */
function localOf(dir: string, item: ImageItem): string {
  const abs = (item.url ?? '').replace(/^file:\/\//i, '')
  if (abs === '') return ''
  const base = dir.replace(/[\\/]+$/, '')
  if (base !== '' && abs.toLowerCase().startsWith(base.toLowerCase() + '\\')) return abs.slice(base.length + 1)
  if (base !== '' && abs.toLowerCase().startsWith(base.toLowerCase() + '/')) return abs.slice(base.length + 1)
  return abs
}

function shortUrl(url: string): string {
  const text = url.replace(/^([a-z][a-z0-9+.-]*):\/\//i, '')
  // data: 后面可能是几 KB 的 base64，塞进一行会把整张表撑坏
  return text.length > 56 ? `${text.slice(0, 56)}…` : text
}

interface DlGroup {
  row: RunRowView
  items: { item: ImageItem; kind: Kind; local: string }[]
  saved: number
  reused: number
  failed: number
  unsupported: number
  pending: number
}

/** 卡片上那条整篇进度轨的宽度；一篇还没开始动时是 0。 */
function progressOf(row: RunRowView): number {
  if (row.total <= 0) return 0
  return Math.min(100, Math.round(((row.done ?? 0) / row.total) * 100))
}

const groups = computed<DlGroup[]>(() => {
  const out: DlGroup[] = []
  for (const row of props.rows) {
    // 跑动中优先用实时明细：Go 侧每下好一张就把那一处改成 localized，
    // 卡片上的「待取回 / 已取回 / 失败」跟着一起动。
    const items = (row.liveItems ?? row.result?.items ?? [])
      .filter((item) => isRemoteRef(item.target))
      .map((item) => ({ item, kind: kindOf(item), local: localOf(row.dir, item) }))
    // 扫完了确实没有远程图的行不必占一张空卡；还没扫通的行必须留着 ——
    // 扫描就发生在卡片里面，卡先出来，内容随后才到。
    if (items.length === 0 && (row.scan ?? 'idle') === 'ok') continue
    out.push({
      row,
      items,
      saved: items.filter((entry) => entry.kind === 'saved').length,
      reused: items.filter((entry) => entry.kind === 'reused').length,
      failed: items.filter((entry) => entry.kind === 'failed').length,
      unsupported: items.filter((entry) => entry.kind === 'unsupported').length,
      pending: items.filter((entry) => entry.kind === 'pending').length,
    })
  }
  const rank = (group: DlGroup): number => ((group.row.scan ?? 'idle') === 'ok' ? 0 : 1)
  return out.sort((a, b) => rank(a) - rank(b))
})

const counts = computed(() => {
  let total = 0
  let saved = 0
  let reused = 0
  let failed = 0
  let unsupported = 0
  let pending = 0
  for (const group of groups.value) {
    for (const entry of group.items) {
      total += 1
      if (entry.kind === 'saved') saved += 1
      else if (entry.kind === 'reused') reused += 1
      else if (entry.kind === 'failed') failed += 1
      else if (entry.kind === 'unsupported') unsupported += 1
      else pending += 1
    }
  }
  return { total, saved, reused, failed, unsupported, pending }
})

const band = computed(() => {
  const cells: { key: MessageKey; count: number; tone: 'plain' | 'ok' | 'warn' | 'bad' }[] = [
    { key: 'download.band.images', count: counts.value.total, tone: 'plain' },
  ]
  if (counts.value.pending > 0) cells.push({ key: 'download.band.pending', count: counts.value.pending, tone: 'plain' })
  if (counts.value.saved > 0) cells.push({ key: 'download.band.saved', count: counts.value.saved, tone: 'ok' })
  if (counts.value.reused > 0) cells.push({ key: 'download.band.reused', count: counts.value.reused, tone: 'ok' })
  if (counts.value.failed > 0) cells.push({ key: 'download.band.failed', count: counts.value.failed, tone: 'bad' })
  if (counts.value.unsupported > 0) cells.push({ key: 'download.band.unsupported', count: counts.value.unsupported, tone: 'warn' })
  return cells
})
</script>

<template>
  <ReportShell
    mode="download"
    icon="download"
    tone="ok"
    title-key="page.download"
    hint-key="view.download.sub"
    :verdicts="band"
    :has-rows="rows.length > 0"
    :recent="recent"
    :busy="busy"
    :progress="progress"
    @pick-files="emit('pick-files')"
    @pick-folder="emit('pick-folder')"
    @pick-recent="emit('pick-recent', $event)"
    @clear="emit('clear')"
  >
    <div class="dl">
      <p v-if="groups.length === 0" class="dl__none">
        {{ t(scanning ? 'download.scanning' : 'download.empty.noRemote', { count: scanCount }) }}
      </p>

      <ResultGroup
        v-for="group in groups"
        :key="group.row.id"
        :name="group.row.name"
        :dir="group.row.dir"
        :scan="group.row.scan ?? 'idle'"
        :scan-error="group.row.scanError"
        :busy="busy"
        :tone="group.failed > 0 ? 'danger' : group.unsupported > 0 ? 'warning' : 'neutral'"
        :expandable="group.items.length > 0"
        @open="openFile(group.row)"
        @reveal="emit('reveal', fileOf(group.row))"
        @remove="emit('remove', group.row.id)"
        @cancel-scan="emit('cancel-scan', group.row.id)"
        @retry-scan="emit('retry-scan', group.row.id)"
      >
        <template #badges>
          <span class="badge">{{ t(plural('row.count', group.items.length), { count: group.items.length }) }}</span>
          <span v-if="group.saved > 0" class="badge badge--ok">
            {{ t('download.group.saved', { count: group.saved }) }}
          </span>
          <span v-if="group.reused > 0" class="badge">
            {{ t('download.group.reused', { count: group.reused }) }}
          </span>
          <span v-if="group.pending > 0" class="badge">
            {{ t('download.group.pending', { count: group.pending }) }}
          </span>
          <span v-if="group.failed > 0" class="badge badge--bad">
            {{ t('download.group.bad', { count: group.failed }) }}
          </span>
          <span v-if="group.unsupported > 0" class="badge badge--warn">
            {{ t('download.group.unsupported', { count: group.unsupported }) }}
          </span>
        </template>
        <template #rail>
          <div
            v-if="group.row.phase === 'running'"
            class="group-card__rail"
            :class="{ 'group-card__rail--wait': group.row.total === 0 }"
          >
            <span :style="group.row.total > 0 ? { width: progressOf(group.row) + '%' } : undefined" />
          </div>
        </template>
        <ul class="dl__list group-rows">
          <li
            v-for="entry in group.items"
            :key="entry.item.target + entry.item.line"
            class="dl__row"
          >
            <span
              class="dl__remote mono"
              :title="`${entry.item.target}\n${t('row.copy')}`"
              role="button"
              tabindex="0"
              @click="copyAddress(entry.item.target)"
              @keydown.enter="copyAddress(entry.item.target)"
            >{{ shortUrl(entry.item.target) }}</span>
            <span class="dl__arrow" aria-hidden="true">→</span>
            <span v-if="entry.local" class="dl__local mono" :title="entry.local">{{ entry.local }}</span>
            <!-- 还没落盘就画一个占位横线：结论由右边的状态标签说，别两处都写「待定」 -->
            <span v-else class="dl__local dl__local--none">—</span>
            <StatusPill
              :label="t(KIND_META[entry.kind].labelKey)"
              :tone="KIND_META[entry.kind].tone"
              :compact="true"
            />
            <span v-if="entry.item.error" class="dl__err">{{ shortReason(entry.item.error) }}</span>
          </li>
        </ul>
      </ResultGroup>
    </div>
    <template #footer>
      <slot name="footer" />
    </template>
  </ReportShell>
</template>

<style scoped>
.dl {
  display: flex;
  flex-direction: column;
}

.dl__none {
  padding: 26px 2px;
  color: var(--ink-3);
  font-size: var(--fs-3);
}


.dl__row {
  display: grid;
  grid-template-columns: minmax(0, 1.02fr) 18px minmax(0, 0.98fr) auto;
  align-items: baseline;
  gap: 14px;
  padding: 13px 16px;
}

.dl__row:hover {
  background: color-mix(in srgb, var(--panel-2) 55%, transparent);
}

.dl__remote,
.dl__local {
  overflow: hidden;
  font-size: var(--fs-3);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dl__remote {
  color: var(--ink-2);
}

.dl__local {
  color: var(--accent-ink);
}

.dl__local--none,

.dl__arrow {
  align-self: center;
  color: var(--ink-3);
  font-size: var(--fs-2);
  text-align: center;
}

/* 失败原因单独一行，从远程地址的左端开始，跟上面那行的地址对齐 */
.dl__err {
  grid-column: 1 / -1;
  margin-top: 4px;
  overflow: hidden;
  color: var(--bad);
  font-size: var(--fs-1);
  line-height: 1.5;
  text-overflow: ellipsis;
}

@media (max-width: 820px) {
  .dl__row {
    grid-template-columns: minmax(0, 1fr) 16px minmax(0, 1fr);
  }
}
</style>
