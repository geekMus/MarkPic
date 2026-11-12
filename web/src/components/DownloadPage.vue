<script setup lang="ts">
import { computed, ref } from 'vue'
import AppIcon from './AppIcon.vue'
import ReportShell from './ReportShell.vue'
import StatusPill from './StatusPill.vue'
import type { ImageItem } from '../types'
import type { RunRowView } from '../composables/useRun'
import { useI18n } from '../i18n'
import type { MessageKey } from '../i18n'
import { copyText, shortReason } from '../composables/useDeadLinks'
import { useToasts } from '../composables/useToasts'

/**
 * 下载页：这一页要回答的是「哪张远程图落到本地哪个文件了」。
 *
 * 所以正文不是文件表，而是一张「远程地址 → 本地文件」的映射：
 * 一个文档一段，每行一张远程图。跑之前本地那一栏写「待定」——
 * 名字要等真正落盘时才知道（重名会被改成 pic-2.png，提前猜就会骗人）。
 */
const props = defineProps<{
  rows: RunRowView[]
  tag?: string
  manualOpen: boolean
  recent?: string[]
  busy?: boolean
  progress?: number
}>()

const emit = defineEmits<{
  'pick-files': []
  'pick-folder': []
  'pick-recent': [path: string]
  'submit-paths': [paths: string[]]
  remove: [id: string]
  clear: []
  'update:manualOpen': [open: boolean]
}>()

const { t } = useI18n()
const { success, danger } = useToasts()

async function copyAddress(url: string): Promise<void> {
  if (await copyText(url)) success(t('toast.copied'))
  else danger(t('toast.copyFailed'))
}

/** 还没扫过张数时先别下结论：显示「正在扫」比显示「没有可下载的图」诚实。 */
const scanning = computed(() => props.rows.some((row) => row.images === null))
const scanCount = computed(() => props.rows.filter((row) => row.images === null).length)

/** 进场错开最多 8 行：再多就一起出现，别让「慢慢浮上来」变成等待。 */

type Kind = 'saved' | 'reused' | 'failed' | 'unsupported' | 'pending'

const KIND_META: Record<Kind, { labelKey: MessageKey; tone: 'neutral' | 'accent' | 'success' | 'warning' | 'danger' }> = {
  saved: { labelKey: 'row.status.localized', tone: 'success' },
  reused: { labelKey: 'row.status.localReused', tone: 'accent' },
  failed: { labelKey: 'row.status.downloadFailed', tone: 'danger' },
  unsupported: { labelKey: 'row.status.remoteUnsupported', tone: 'warning' },
  pending: { labelKey: 'download.row.pending', tone: 'neutral' },
}

function isRemoteRef(target: string): boolean {
  return /^[a-z][a-z0-9+.-]*:/i.test(target) && !/^[a-z]:[\\/]/i.test(target)
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
  /** 折叠状态：和上传页的卡一样，默认收起，点卡头展开。 */
const openIds = ref<Set<string>>(new Set())

function isOpen(id: string): boolean {
  return openIds.value.has(id)
}

function toggle(id: string): void {
  const next = new Set(openIds.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  openIds.value = next
}

/** 卡头只写最后一级目录：完整路径在悬停的 title 里。 */
function shortDir(dir: string): string {
  const parts = dir.split(/[\\/]/).filter((part) => part !== '')
  return parts.length > 0 ? parts[parts.length - 1] : dir
}

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
  failed: number
  unsupported: number
}

const groups = computed<DlGroup[]>(() => {
  const out: DlGroup[] = []
  for (const row of props.rows) {
    const items = (row.result?.items ?? [])
      .filter((item) => isRemoteRef(item.target))
      .map((item) => ({ item, kind: kindOf(item), local: localOf(row.dir, item) }))
    if (items.length === 0) continue
    out.push({
      row,
      items,
      failed: items.filter((entry) => entry.kind === 'failed').length,
      unsupported: items.filter((entry) => entry.kind === 'unsupported').length,
    })
  }
  return out
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
    :tag="tag"
    :verdicts="band"
    :has-rows="rows.length > 0"
    :manual-open="manualOpen"
    :recent="recent"
    :busy="busy"
    :progress="progress"
    :indeterminate="rows.length <= 1"
    @pick-files="emit('pick-files')"
    @pick-folder="emit('pick-folder')"
    @pick-recent="emit('pick-recent', $event)"
    @submit-paths="emit('submit-paths', $event)"
    @clear="emit('clear')"
    @update:manual-open="emit('update:manualOpen', $event)"
  >
    <div class="dl">
      <p v-if="groups.length === 0" class="dl__none">
        {{ t(scanning ? 'download.scanning' : 'download.empty.noRemote', { count: scanCount }) }}
      </p>

      <section
        v-for="group in groups"
        :key="group.row.id"
        class="group-card"
      >
        <header
          class="group-card__head group-card__head--fold"
          role="button"
          tabindex="0"
          :aria-expanded="isOpen(group.row.id)"
          @click="toggle(group.row.id)"
          @keydown.enter.prevent="toggle(group.row.id)"
          @keydown.space.prevent="toggle(group.row.id)"
        >
          <span
            class="group-card__mark"
            :data-tone="group.failed > 0 ? 'danger' : group.unsupported > 0 ? 'warning' : 'neutral'"
            aria-hidden="true"
          >
            <AppIcon name="doc" :size="15" />
          </span>
          <span class="group-card__text">
            <span class="group-card__name" :title="group.row.path">{{ group.row.name }}</span>
            <span class="group-card__sub mono" :title="group.row.dir">{{ shortDir(group.row.dir) }}</span>
          </span>
          <span class="group-card__badges">
            <span class="badge">{{ t('row.count', { count: group.items.length }) }}</span>
            <span v-if="group.failed > 0" class="badge badge--bad">
              {{ t('download.group.bad', { count: group.failed }) }}
            </span>
            <span v-if="group.unsupported > 0" class="badge badge--warn">
              {{ t('download.group.unsupported', { count: group.unsupported }) }}
            </span>
          </span>
          <span class="group-card__more" :class="{ 'group-card__more--on': isOpen(group.row.id) }">
            {{ t(isOpen(group.row.id) ? 'row.details.collapse' : 'row.details.expand') }}
            <AppIcon class="group-card__chev" name="chevron-down" :size="13" />
          </span>
          <button
            type="button"
            class="icon-btn icon-btn--sm dl__drop"
            :title="t('row.remove')"
            :aria-label="t('row.remove')"
            @click.stop="emit('remove', group.row.id)"
          >
            <AppIcon name="x" :size="13" />
          </button>
        </header>
        <div class="group-card__fold" :class="{ 'group-card__fold--open': isOpen(group.row.id) }">
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
        </div>
      </section>
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


.dl__drop {
  flex: none;
  opacity: 0;
  transition: opacity 140ms var(--ease);
}

.group-card:hover .dl__drop,
.dl__drop:focus-visible {
  opacity: 1;
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
