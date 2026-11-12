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
 * 检测页：和「上传」一样按文档分卡片 —— 一篇文档一张卡，卡里是这篇文档的
 * 远程地址。同一个地址在别处也出现过时，卡片里只看这一篇自己的位置；
 * 顶上的小结才按地址去重（后端也是按地址探活的）。
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
  clear: []
  'update:manualOpen': [open: boolean]
}>()

const { t } = useI18n()
const { success, danger } = useToasts()

/** 一行地址点一下就进剪贴板：贴到别处查比手动选中省事。 */
async function copyAddress(url: string): Promise<void> {
  if (await copyText(url)) success(t('toast.copied'))
  else danger(t('toast.copyFailed'))
}

/** 进场错开最多 8 行：再多就一起出现，别让「慢慢浮上来」变成等待。 */

type Kind = 'bad' | 'unsupported' | 'pending' | 'ok'

const KIND_META: Record<Kind, { labelKey: MessageKey; tone: 'neutral' | 'accent' | 'success' | 'warning' | 'danger' }> = {
  bad: { labelKey: 'row.status.remoteBad', tone: 'danger' },
  unsupported: { labelKey: 'row.status.remoteUnsupported', tone: 'warning' },
  pending: { labelKey: 'check.row.pending', tone: 'neutral' },
  ok: { labelKey: 'row.status.remoteOk', tone: 'success' },
}

/** 排在前面的是更该看的：打不开 → 碰不了 → 还没测 → 一切正常。 */
const RANK: Record<Kind, number> = { bad: 0, unsupported: 1, pending: 2, ok: 3 }

interface AddrRow {
  url: string
  label: string
  kind: Kind
  error: string
}

/** 远程引用（http、ftp、data: 都算），但要排除 Windows 盘符 `D:\…`。 */
function isRemoteRef(target: string): boolean {
  return /^[a-z][a-z0-9+.-]*:/i.test(target) && !/^[a-z]:[\\/]/i.test(target)
}

/**
 * 卡里没有域名做上下文了，所以 http(s) 的地址连主机一起写（127.0.0.1:5199/missing.png）；
 * data: 这种没有主机的就整条写，长度先截一下，完整值在悬停的 title 里。
 */
function labelOf(target: string): string {
  // data: 的正文是一整段 base64，留在标签里只会把行撑坏：只写类型。
  const data = /^data:([^;,]*)[^,]*/i.exec(target)
  if (data) return `data:${data[1]};…`
  const http = /^https?:\/\/([^/?#]*)(.*)$/i.exec(target)
  const label = http ? http[1] + (http[2] || '/') : target
  return label.length > 90 ? label.slice(0, 86) + '…' : label
}

function kindOf(item: ImageItem): Kind {
  if (item.status === 'remote-ok') return 'ok'
  if (item.status === 'remote-bad') return 'bad'
  // status=remote 且带 error 的是碰不了的地址（ftp://、data:），没 error 的是还没轮到。
  if (item.status === 'remote' && item.error) return 'unsupported'
  return 'pending'
}

/** 顶上的小结按「地址」去重（同一地址在多篇文档里只算一个），卡片才按文档分。 */
const addresses = computed<{ url: string; kind: Kind }[]>(() => {
  const byUrl = new Map<string, Kind>()
  for (const row of props.rows) {
    for (const item of row.result?.items ?? []) {
      if (!isRemoteRef(item.target)) continue
      const kind = kindOf(item)
      const known = byUrl.get(item.target)
      // 同一个地址被多处引用，状态可能不一致（一处探活过、一处还没轮到）：坏的优先。
      if (known === undefined || RANK[kind] < RANK[known]) byUrl.set(item.target, kind)
    }
  }
  return [...byUrl.entries()].map(([url, kind]) => ({ url, kind }))
})

const filter = ref<'all' | 'bad' | 'ok'>('all')

const counts = computed(() => {
  const list = addresses.value
  return {
    all: list.length,
    bad: list.filter((a) => a.kind === 'bad').length,
    unsupported: list.filter((a) => a.kind === 'unsupported').length,
    ok: list.filter((a) => a.kind === 'ok').length,
    pending: list.filter((a) => a.kind === 'pending').length,
  }
})

interface DocGroup {
  id: string
  name: string
  dir: string
  list: DocAddr[]
  bad: number
  unsupported: number
}

/** 一行地址在某一篇文档里的位置：同一个地址在同篇里重复引用时，行号收在一起。 */
interface DocAddr extends AddrRow {
  lines: number[]
}

/**
 * 按文档分组：一篇文档一张卡。组内地址按「坏的先」排，卡与卡之间
 * 先看哪篇问题多 —— 出问题的文档应该在上面。
 */
const groups = computed<DocGroup[]>(() => {
  const out: DocGroup[] = []
  for (const row of props.rows) {
    const byTarget = new Map<string, DocAddr>()
    for (const item of row.result?.items ?? []) {
      if (!isRemoteRef(item.target)) continue
      const kind = kindOf(item)
      const known = byTarget.get(item.target)
      if (known) {
        if (!known.lines.includes(item.line)) known.lines.push(item.line)
        // 同篇里同址状态不一致（一处探过、一处没轮到）：坏的优先。
        if (RANK[kind] < RANK[known.kind]) {
          known.kind = kind
          known.error = item.error ?? ''
        }
        continue
      }
      byTarget.set(item.target, {
        url: item.target,
        label: labelOf(item.target),
        kind,
        error: item.error ?? '',
        lines: [item.line],
      })
    }
    if (byTarget.size === 0) continue
    const list = [...byTarget.values()].sort(
      (a, b) => RANK[a.kind] - RANK[b.kind] || a.url.localeCompare(b.url),
    )
    out.push({
      id: row.id,
      name: row.name,
      dir: row.dir,
      list,
      bad: list.filter((a) => a.kind === 'bad').length,
      unsupported: list.filter((a) => a.kind === 'unsupported').length,
    })
  }
  return out.sort((a, b) => b.bad - a.bad || b.list.length - a.list.length || a.name.localeCompare(b.name))
})

/** 卡头只写最后一级目录，和上传页的卡片一个规矩。 */
function shortDir(dir: string): string {
  const parts = dir.split(/[\\/]/).filter((part) => part !== '')
  return parts.length > 0 ? parts[parts.length - 1] : dir
}

/** 筛掉不匹配的行后，空了的组不再显示。 */
const shownGroups = computed<DocGroup[]>(() => {
  if (filter.value === 'all') return groups.value
  const want: Kind = filter.value === 'bad' ? 'bad' : 'ok'
  return groups.value
    .map((group) => ({ ...group, list: group.list.filter((a) => a.kind === want) }))
    .filter((group) => group.list.length > 0)
})

const band = computed(() => {
  const cells: { key: MessageKey; count: number; tone: 'plain' | 'ok' | 'warn' | 'bad' }[] = [
    { key: 'check.band.addresses', count: counts.value.all, tone: 'plain' },
  ]
  if (counts.value.pending > 0) cells.push({ key: 'check.band.pending', count: counts.value.pending, tone: 'plain' })
  if (counts.value.ok > 0) cells.push({ key: 'check.band.ok', count: counts.value.ok, tone: 'ok' })
  if (counts.value.bad > 0) cells.push({ key: 'check.band.bad', count: counts.value.bad, tone: 'bad' })
  if (counts.value.unsupported > 0) {
    cells.push({ key: 'check.band.unsupported', count: counts.value.unsupported, tone: 'warn' })
  }
  return cells
})

/**
 * 还在扫的时候不能说「这些文件里没有远程图片」——扫完才知道。
 * 检测模式的预检本身就要访问网络，慢的时候要等十几秒。
 */
const scanning = computed(() => props.rows.some((row) => row.images === null))
const scanCount = computed(() => props.rows.filter((row) => row.images === null).length)

const emptyHint = computed<MessageKey>(() => {
  if (scanning.value) return 'check.scanning'
  return addresses.value.length === 0 ? 'check.empty.noRemote' : 'check.empty.filtered'
})
</script>

<template>
  <ReportShell
    mode="check"
    icon="link"
    tone="warn"
    title-key="page.check"
    hint-key="view.check.sub"
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
    <div class="chk">
      <div v-if="counts.all > 0" class="chk__filters" role="group" :aria-label="t('check.filter.label')">
        <button
          type="button"
          class="chk__chip"
          :aria-pressed="filter === 'all'"
          @click="filter = 'all'"
        >
          {{ t('check.filter.all') }} <span class="num">{{ counts.all }}</span>
        </button>
        <button
          type="button"
          class="chk__chip chk__chip--bad"
          :aria-pressed="filter === 'bad'"
          :disabled="counts.bad === 0"
          @click="filter = 'bad'"
        >
          {{ t('check.filter.bad') }} <span class="num">{{ counts.bad }}</span>
        </button>
        <button
          type="button"
          class="chk__chip chk__chip--ok"
          :aria-pressed="filter === 'ok'"
          :disabled="counts.ok === 0"
          @click="filter = 'ok'"
        >
          {{ t('check.filter.ok') }} <span class="num">{{ counts.ok }}</span>
        </button>
      </div>

      <p v-if="shownGroups.length === 0" class="chk__none">{{ t(emptyHint, { count: scanCount }) }}</p>

      <section
        v-for="group in shownGroups"
        :key="group.id"
        class="group-card"
      >
        <header class="group-card__head">
          <span class="group-card__icon" aria-hidden="true">
            <AppIcon name="doc" :size="14" />
          </span>
          <h3 class="group-card__title" :title="group.name">{{ group.name }}</h3>
          <span class="chk__dir mono">{{ shortDir(group.dir) }}</span>
          <span class="group-card__meta">{{ t('check.group.count', { count: group.list.length }) }}</span>
          <span v-if="group.bad > 0" class="group-card__meta group-card__meta--bad">
            {{ t('check.group.bad', { count: group.bad }) }}
          </span>
          <span v-if="group.unsupported > 0" class="group-card__meta group-card__meta--warn">
            {{ t('check.group.unsupported', { count: group.unsupported }) }}
          </span>
        </header>
        <ul class="chk__list group-rows">
          <li
            v-for="addr in group.list"
            :key="addr.url"
            class="chk__row"
          >
            <StatusPill
              :label="t(KIND_META[addr.kind].labelKey)"
              :tone="KIND_META[addr.kind].tone"
              :compact="true"
            />
            <span
              class="chk__addr mono"
              :title="`${addr.url}\n${t('row.copy')}`"
              role="button"
              tabindex="0"
              @click="copyAddress(addr.url)"
              @keydown.enter="copyAddress(addr.url)"
            >{{ addr.label }}</span>
            <span class="chk__src mono" :title="addr.url">
              {{ addr.lines.map((line) => `L${line}`).join(' · ') }}
            </span>
            <span
              v-if="addr.error"
              class="chk__err"
              :class="{ 'chk__err--warn': addr.kind === 'unsupported' }"
            >{{ shortReason(addr.error) }}</span>
          </li>
        </ul>
      </section>
    </div>
    <template #footer>
      <slot name="footer" />
    </template>
  </ReportShell>
</template>

<style scoped>
.chk {
  display: flex;
  flex-direction: column;
}

/* 筛选片：不吸顶，跟正文一起滚，省掉那条挡视线的底色 */
.chk__filters {
  display: flex;
  flex: none;
  gap: 8px;
  margin-bottom: 24px;
}

.chk__chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 26px;
  padding: 0 11px;
  border: 1px solid var(--line);
  border-radius: var(--r-pill);
  background: transparent;
  color: var(--ink-3);
  font-size: var(--fs-2);
  font-weight: 500;
  cursor: pointer;
  transition:
    background var(--t-fast) var(--ease),
    border-color var(--t-fast) var(--ease),
    color var(--t-fast) var(--ease);
}

.chk__chip:hover:not(:disabled):not([aria-pressed='true']) {
  color: var(--ink);
  border-color: var(--line-2);
}

.chk__chip[aria-pressed='true'] {
  border-color: var(--line-2);
  background: var(--panel-2);
  color: var(--ink);
}

.chk__chip:disabled {
  opacity: 0.45;
  cursor: default;
}

.chk__chip .num {
  color: var(--ink-3);
  font-size: var(--fs-1);
}

.chk__chip--bad[aria-pressed='true'] {
  border-color: var(--bad-line);
  background: var(--bad-soft);
  color: var(--bad);
}

.chk__chip--bad[aria-pressed='true'] .num {
  color: var(--bad);
}

.chk__chip--ok[aria-pressed='true'] {
  border-color: var(--ok-line);
  background: var(--ok-soft);
  color: var(--ok);
}

.chk__chip--ok[aria-pressed='true'] .num {
  color: var(--ok);
}

.chk__none {
  padding: 26px 2px;
  color: var(--ink-3);
  font-size: var(--fs-3);
}

/* 组头里的目录：只留最后一级，和上传页卡片一个规矩 */
.chk__dir {
  overflow: hidden;
  flex: 0 1 auto;
  max-width: 34%;
  color: var(--ink-3);
  font-size: var(--fs-1);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.chk__row {
  display: grid;
  grid-template-columns: 82px minmax(0, 1fr) auto;
  align-items: baseline;
  gap: 14px;
  padding: 13px 16px;
}

.chk__row:hover {
  background: color-mix(in srgb, var(--panel-2) 55%, transparent);
}

.chk__addr {
  overflow: hidden;
  cursor: copy;
  color: var(--ink);
  font-size: var(--fs-3);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.chk__addr:hover {
  color: var(--ink);
  text-decoration: underline dotted;
}

.chk__src {
  color: var(--ink-3);
  font-size: var(--fs-1);
  white-space: nowrap;
}

/* 打不开的原因（404、证书过期）单独一行，跟地址左端对齐 */
.chk__err {
  grid-column: 2 / -1;
  margin-top: 4px;
  overflow: hidden;
  color: var(--bad);
  font-size: var(--fs-1);
  line-height: 1.5;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 没法处理（ftp、data: 这类）不算「坏」，用琥珀色，跟结论那一句一致 */
.chk__err--warn {
  color: var(--warn);
}
</style>
