<script setup lang="ts">
import { computed, ref } from 'vue'
import ReportShell from './ReportShell.vue'
import StatusPill from './StatusPill.vue'
import type { ImageItem } from '../types'
import type { RunRowView } from '../composables/useRun'
import { useI18n } from '../i18n'
import type { MessageKey } from '../i18n'

/**
 * 检测页：这一页关心的不是「哪个文件」，而是「哪个地址还活着」。
 *
 * 所以正文是一张按域名分组的地址表，而不是文件队列：一行一个地址，
 * 一行写清它在几个文件里被引用。同一个地址在多篇文档里重复出现时只占一行
 * （后端也是按地址去重探活的）。
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
  clear: []
  'update:manualOpen': [open: boolean]
}>()

const { t } = useI18n()

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
  host: string
  label: string
  kind: Kind
  error: string
  sources: { name: string; line: number }[]
}

/** 远程引用（http、ftp、data: 都算），但要排除 Windows 盘符 `D:\…`。 */
function isRemoteRef(target: string): boolean {
  return /^[a-z][a-z0-9+.-]*:/i.test(target) && !/^[a-z]:[\\/]/i.test(target)
}

function splitUrl(target: string): { host: string; label: string } {
  const full = /^([a-z][a-z0-9+.-]*):\/\/([^/?#]*)(.*)$/i.exec(target)
  if (full) {
    const host = full[2] || full[1]
    const rest = full[3] || '/'
    return { host: host.toLowerCase(), label: rest }
  }
  const scheme = /^([a-z][a-z0-9+.-]*):/i.exec(target)
  const name = scheme ? scheme[1].toLowerCase() + ':' : '…'
  const rest = target.slice(scheme ? scheme[0].length : 0)
  // data: 之类没有主机名的地址可能是一整段 base64：只留个头，正文里另有完整值可查
  const label = rest.length > 60 ? rest.slice(0, 56) + '…' : rest
  return { host: name, label: label === '' ? target : label }
}

function kindOf(item: ImageItem): Kind {
  if (item.status === 'remote-ok') return 'ok'
  if (item.status === 'remote-bad') return 'bad'
  // status=remote 且带 error 的是碰不了的地址（ftp://、data:），没 error 的是还没轮到。
  if (item.status === 'remote' && item.error) return 'unsupported'
  return 'pending'
}

const addresses = computed<AddrRow[]>(() => {
  const byUrl = new Map<string, AddrRow>()
  for (const row of props.rows) {
    for (const item of row.result?.items ?? []) {
      if (!isRemoteRef(item.target)) continue
      const known = byUrl.get(item.target)
      const kind = kindOf(item)
      if (!known) {
        const { host, label } = splitUrl(item.target)
        byUrl.set(item.target, {
          url: item.target,
          host,
          label,
          kind,
          error: item.error ?? '',
          sources: [{ name: row.name, line: item.line }],
        })
        continue
      }
      // 同一个地址被多处引用，状态可能不一致（一处探活过、一处还没轮到）：坏的优先。
      if (RANK[kind] < RANK[known.kind]) {
        known.kind = kind
        known.error = item.error ?? ''
      }
      known.sources.push({ name: row.name, line: item.line })
    }
  }
  return [...byUrl.values()].sort((a, b) => RANK[a.kind] - RANK[b.kind] || a.url.localeCompare(b.url))
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

const shown = computed(() => {
  if (filter.value === 'bad') return addresses.value.filter((a) => a.kind === 'bad')
  if (filter.value === 'ok') return addresses.value.filter((a) => a.kind === 'ok')
  return addresses.value
})

const groups = computed(() => {
  const byHost = new Map<string, AddrRow[]>()
  for (const addr of shown.value) {
    const list = byHost.get(addr.host)
    if (list) list.push(addr)
    else byHost.set(addr.host, [addr])
  }
  return [...byHost.entries()]
    .map(([host, list]) => ({
      host,
      list,
      bad: list.filter((a) => a.kind === 'bad').length,
      unsupported: list.filter((a) => a.kind === 'unsupported').length,
    }))
    .sort((a, b) => b.bad - a.bad || b.list.length - a.list.length || a.host.localeCompare(b.host))
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

function sourceTitle(addr: AddrRow): string {
  return addr.sources.map((s) => `${s.name}:${s.line}`).join('\n')
}

const emptyHint = computed<MessageKey>(() =>
  addresses.value.length === 0 ? 'check.empty.noRemote' : 'check.empty.filtered',
)
</script>

<template>
  <ReportShell
    mode="check"
    icon="link"
    title-key="nav.check"
    hint-key="view.check.sub"
    :tag="tag"
    :count="counts.all"
    unit-key="check.unit"
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

      <p v-if="groups.length === 0" class="chk__none">{{ t(emptyHint) }}</p>

      <section v-for="group in groups" :key="group.host" class="chk__group">
        <header class="chk__host">
          <h3>{{ group.host }}</h3>
          <span class="chk__meta">{{ t('check.group.count', { count: group.list.length }) }}</span>
          <span v-if="group.bad > 0" class="chk__meta chk__meta--bad">
            {{ t('check.group.bad', { count: group.bad }) }}
          </span>
          <span v-if="group.unsupported > 0" class="chk__meta chk__meta--warn">
            {{ t('check.group.unsupported', { count: group.unsupported }) }}
          </span>
        </header>
        <ul class="chk__list">
          <li v-for="addr in group.list" :key="addr.url" class="chk__row">
            <StatusPill
              :label="t(KIND_META[addr.kind].labelKey)"
              :tone="KIND_META[addr.kind].tone"
              :compact="true"
            />
            <span class="chk__addr mono" :title="addr.url">{{ addr.label }}</span>
            <span class="chk__src" :title="sourceTitle(addr)">
              {{ t('check.row.sources', { count: addr.sources.length }) }}
            </span>
            <span
              v-if="addr.error"
              class="chk__err"
              :class="{ 'chk__err--warn': addr.kind === 'unsupported' }"
            >{{ addr.error }}</span>
          </li>
        </ul>
      </section>
    </div>
  </ReportShell>
</template>

<style scoped>
.chk {
  padding: 0 18px 10px;
}

.chk__filters {
  position: sticky;
  top: 0;
  z-index: 2;
  display: flex;
  gap: 6px;
  padding: 10px 0 9px;
  background: var(--panel);
}

.chk__chip {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 24px;
  padding: 0 9px;
  border: 1px solid var(--line);
  border-radius: var(--r-pill);
  background: transparent;
  color: var(--ink-3);
  font-size: var(--fs-1);
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
  font-size: 10.5px;
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
  padding: 22px 2px;
  color: var(--ink-3);
  font-size: var(--fs-2);
}

.chk__host {
  position: sticky;
  top: 43px;
  z-index: 1;
  display: flex;
  align-items: baseline;
  gap: 8px;
  padding: 8px 2px 7px;
  background: var(--panel);
}

.chk__host h3 {
  color: var(--ink-2);
  font-size: var(--fs-2);
  font-weight: 600;
}

.chk__meta {
  color: var(--ink-3);
  font-size: var(--fs-1);
}

.chk__meta--bad {
  color: var(--bad);
}

.chk__meta--warn {
  color: var(--warn);
}

.chk__list {
  display: flex;
  flex-direction: column;
  margin: 0;
  padding: 0;
  list-style: none;
}

.chk__row {
  display: grid;
  grid-template-columns: 82px minmax(0, 1fr) auto;
  align-items: baseline;
  gap: 10px;
  padding: 7px 8px 7px 6px;
  border-radius: var(--r-2);
}

.chk__row:hover {
  background: var(--panel-2);
}

.chk__addr {
  overflow: hidden;
  color: var(--ink);
  font-size: var(--fs-2);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.chk__src {
  color: var(--ink-3);
  font-size: var(--fs-1);
  white-space: nowrap;
}

/* 打不开的原因（404、证书过期）单独一行，小一号，跟着地址缩 */
.chk__err {
  grid-column: 2 / -1;
  overflow: hidden;
  color: var(--bad);
  font-size: var(--fs-1);
  line-height: 1.45;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 没法处理（ftp、data: 这类）不算「坏」，用琥珀色，跟结论带那一格一致 */
.chk__err--warn {
  color: var(--warn);
}
</style>