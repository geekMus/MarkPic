<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import ReportShell from './ReportShell.vue'
import StatusPill from './StatusPill.vue'
import type { ImageItem } from '../types'
import type { RunRowView } from '../composables/useRun'
import { useI18n } from '../i18n'
import type { MessageKey } from '../i18n'

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

function kindOf(item: ImageItem): Kind {
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
  return url.replace(/^([a-z][a-z0-9+.-]*):\/\//i, '')
}

const groups = computed(() => {
  const out: { row: RunRowView; items: { item: ImageItem; kind: Kind; local: string }[] }[] = []
  for (const row of props.rows) {
    const items = (row.result?.items ?? [])
      .filter((item) => isRemoteRef(item.target))
      .map((item) => ({ item, kind: kindOf(item), local: localOf(row.dir, item) }))
    if (items.length > 0) out.push({ row, items })
  }
  return out
})

const counts = computed(() => {
  let total = 0
  let saved = 0
  let reused = 0
  let failed = 0
  let pending = 0
  for (const group of groups.value) {
    for (const entry of group.items) {
      total += 1
      if (entry.kind === 'saved') saved += 1
      else if (entry.kind === 'reused') reused += 1
      else if (entry.kind === 'failed') failed += 1
      else pending += 1
    }
  }
  return { total, saved, reused, failed, pending }
})

const band = computed(() => {
  const cells: { key: MessageKey; count: number; tone: 'plain' | 'ok' | 'warn' | 'bad' }[] = [
    { key: 'download.band.images', count: counts.value.total, tone: 'plain' },
  ]
  if (counts.value.pending > 0) cells.push({ key: 'download.band.pending', count: counts.value.pending, tone: 'plain' })
  if (counts.value.saved > 0) cells.push({ key: 'download.band.saved', count: counts.value.saved, tone: 'ok' })
  if (counts.value.reused > 0) cells.push({ key: 'download.band.reused', count: counts.value.reused, tone: 'ok' })
  if (counts.value.failed > 0) cells.push({ key: 'download.band.failed', count: counts.value.failed, tone: 'bad' })
  return cells
})
</script>

<template>
  <ReportShell
    mode="download"
    icon="download"
    tone="ok"
    title-key="nav.download"
    hint-key="view.download.sub"
    :tag="tag"
    :verdicts="band"
    :has-rows="rows.length > 0"
    :manual-open="manualOpen"
    :recent="recent"
    @pick-files="emit('pick-files')"
    @pick-folder="emit('pick-folder')"
    @pick-recent="emit('pick-recent', $event)"
    @submit-paths="emit('submit-paths', $event)"
    @clear="emit('clear')"
    @update:manual-open="emit('update:manualOpen', $event)"
  >
    <div class="dl">
      <p v-if="groups.length === 0" class="dl__none">{{ t('download.empty.noRemote') }}</p>

      <section v-for="group in groups" :key="group.row.id" class="dl__group">
        <header class="dl__file">
          <h3 class="dl__name" :title="group.row.path">{{ group.row.name }}</h3>
          <span class="dl__dir" :title="group.row.dir">{{ group.row.dir }}</span>
          <span class="dl__meta">{{ t('row.count', { count: group.items.length }) }}</span>
          <button
            type="button"
            class="icon-btn icon-btn--sm dl__drop"
            :title="t('row.remove')"
            :aria-label="t('row.remove')"
            @click="emit('remove', group.row.id)"
          >
            <AppIcon name="x" :size="13" />
          </button>
        </header>
        <ul class="dl__list">
          <li v-for="entry in group.items" :key="entry.item.target + entry.item.line" class="dl__row">
            <span class="dl__remote mono" :title="entry.item.target">{{ shortUrl(entry.item.target) }}</span>
            <span class="dl__arrow" aria-hidden="true">→</span>
            <span v-if="entry.local" class="dl__local mono" :title="entry.local">{{ entry.local }}</span>
            <span v-else-if="entry.kind === 'failed' || entry.kind === 'unsupported'" class="dl__local dl__local--none">—</span>
            <span v-else class="dl__local dl__local--todo">{{ t('download.row.pending') }}</span>
            <StatusPill
              :label="t(KIND_META[entry.kind].labelKey)"
              :tone="KIND_META[entry.kind].tone"
              :compact="true"
            />
            <span v-if="entry.item.error" class="dl__err">{{ entry.item.error }}</span>
          </li>
        </ul>
      </section>
    </div>
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

/* 一个文档一段：段与段之间留白，不画框 */
.dl__group + .dl__group {
  margin-top: 30px;
}

.dl__file {
  display: flex;
  align-items: baseline;
  gap: 10px;
  padding: 0 10px 10px;
  border-bottom: 1px solid var(--line);
}

.dl__name {
  max-width: 42%;
  overflow: hidden;
  color: var(--ink-2);
  font-size: var(--fs-2);
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dl__dir {
  overflow: hidden;
  min-width: 0;
  color: var(--ink-3);
  font-family: var(--mono);
  font-size: var(--fs-1);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dl__meta {
  flex: none;
  color: var(--ink-3);
  font-size: var(--fs-1);
}

.dl__drop {
  flex: none;
  margin-left: auto;
  opacity: 0;
}

.dl__file:hover .dl__drop,
.dl__drop:focus-visible {
  opacity: 1;
}

.dl__list {
  display: flex;
  flex-direction: column;
  margin: 0;
  padding: 6px 0 0;
  list-style: none;
}

.dl__row {
  display: grid;
  grid-template-columns: minmax(0, 1.02fr) 18px minmax(0, 0.98fr) auto;
  align-items: baseline;
  gap: 14px;
  padding: 13px 12px 13px 10px;
  border-radius: var(--r-2);
}

.dl__row:hover {
  background: var(--panel-2);
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
.dl__local--todo {
  color: var(--ink-3);
}

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
  .dl__dir {
    display: none;
  }

  .dl__row {
    grid-template-columns: minmax(0, 1fr) 16px minmax(0, 1fr);
  }
}
</style>
