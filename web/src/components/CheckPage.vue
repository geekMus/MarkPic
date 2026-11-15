<script setup lang="ts">
import { computed, ref } from 'vue'
import AppIcon from './AppIcon.vue'
import ReportShell from './ReportShell.vue'
import ScanBadge from './ScanBadge.vue'
import StatusPill from './StatusPill.vue'
import type { ImageItem } from '../types'
import type { RunRowView, ScanState } from '../composables/useRun'
import { useI18n } from '../i18n'
import type { MessageKey } from '../i18n'
import { copyText, shortReason } from '../composables/useDeadLinks'
import { useToasts } from '../composables/useToasts'
import { describeError, openTarget } from '../api'

/**
 * 检测页：和「上传」一样按文档分卡片 —— 一篇文档一张卡，卡里是这篇文档的
 * 远程地址。同一个地址在别处也出现过时，卡片里只看这一篇自己的位置；
 * 顶上的小结才按地址去重（后端也是按地址探活的）。
 */
const props = defineProps<{
  rows: RunRowView[]
  tag?: string
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
    for (const item of row.liveItems ?? row.result?.items ?? []) {
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
  /** 原文档：检测是只读的，卡头上「打开文件」开的就是它。 */
  path: string
  list: DocAddr[]
  ok: number
  bad: number
  unsupported: number
  pending: number
  /** 这张卡自己扫通了没有：没通的时候卡里说的全是扫描，不是地址。 */
  scan: ScanState
  scanError?: string
}

/** 一行地址在某一篇文档里的位置：同一个地址在同篇里重复引用时，行号收在一起。 */
interface DocAddr extends AddrRow {
  lines: number[]
}

/**
 * 按文档分组：一篇文档一张卡。组内地址按「坏的先」排，卡与卡之间
 * 先看哪篇问题多 —— 出问题的文档应该在上面。
 *
 * 还在一份份扫的文档也有卡：扫描这件事发生在卡片里面，不是先在页顶上
 * 挂一行「正在扫描 3 个文件」、扫完了才让卡片出现。
 */
const groups = computed<DocGroup[]>(() => {
  const out: DocGroup[] = []
  for (const row of props.rows) {
    const byTarget = new Map<string, DocAddr>()
    // 跑动中用实时明细：每检测完一个地址，卡片上的结论与计数跟着换。
    for (const item of row.liveItems ?? row.result?.items ?? []) {
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
    const list = [...byTarget.values()].sort(
      (a, b) => RANK[a.kind] - RANK[b.kind] || a.url.localeCompare(b.url),
    )
    out.push({
      id: row.id,
      name: row.name,
      dir: row.dir,
      path: row.path,
      list,
      ok: list.filter((a) => a.kind === 'ok').length,
      bad: list.filter((a) => a.kind === 'bad').length,
      unsupported: list.filter((a) => a.kind === 'unsupported').length,
      pending: list.filter((a) => a.kind === 'pending').length,
      scan: row.scan ?? 'idle',
      scanError: row.scanError,
    })
  }
  const rank = (group: DocGroup): number => (group.scan === 'ok' ? 0 : 1)
  return out.sort(
    (a, b) =>
      rank(a) - rank(b) ||
      b.bad - a.bad ||
      b.list.length - a.list.length ||
      a.name.localeCompare(b.name),
  )
})

/** 卡头只写最后一级目录，和上传页的卡片一个规矩。 */
function shortDir(dir: string): string {
  const parts = dir.split(/[\\/]/).filter((part) => part !== '')
  return parts.length > 0 ? parts[parts.length - 1] : dir
}

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

/**
 * 卡头上的「打开文件」：检测只读，一份文件都没改写，所以打开的就是原文档 ——
 * 打不开的地址在哪一行已经写在卡里，点开就能去改。打开失败要说原因，
 * 别让按钮看起来像没反应。
 */
async function openFile(path: string): Promise<void> {
  if (path === '') return
  try {
    await openTarget(path)
  } catch (error) {
    danger(describeError(error, t('error.openOutput')))
  }
}

/**
 * 筛掉不匹配的行后，空了的组不再显示。
 *
 * 例外是还没扫通的卡：它们本来就没有地址可筛，筛掉等于让人以为那份文档
 * 不存在了 —— 正在扫的文件必须在眼前出现，哪怕内容还是空的。
 */
const shownGroups = computed<DocGroup[]>(() => {
  if (filter.value === 'all') return groups.value
  const want: Kind = filter.value === 'bad' ? 'bad' : 'ok'
  return groups.value
    .map((group) => ({ ...group, list: group.list.filter((a) => a.kind === want) }))
    .filter((group) => group.list.length > 0 || group.scan !== 'ok')
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
const scanning = computed(() => props.rows.some((row) => (row.scan ?? 'idle') === 'scanning'))
const scanCount = computed(() => props.rows.filter((row) => (row.scan ?? 'idle') === 'scanning').length)

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
    :recent="recent"
    :busy="busy"
    :progress="progress"
    @pick-files="emit('pick-files')"
    @pick-folder="emit('pick-folder')"
    @pick-recent="emit('pick-recent', $event)"
    @clear="emit('clear')"
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
        <header
          class="group-card__head group-card__head--fold"
          role="button"
          tabindex="0"
          :aria-expanded="isOpen(group.id)"
          @click="toggle(group.id)"
          @keydown.enter.prevent="toggle(group.id)"
          @keydown.space.prevent="toggle(group.id)"
        >
          <span
            class="group-card__mark"
            :data-tone="group.bad > 0 ? 'danger' : group.unsupported > 0 ? 'warning' : 'neutral'"
            aria-hidden="true"
          >
            <AppIcon name="doc" :size="15" />
          </span>
          <span class="group-card__text">
            <span class="group-card__name" :title="group.name">{{ group.name }}</span>
            <span class="group-card__sub mono" :title="group.dir">{{ shortDir(group.dir) }}</span>
          </span>
          <span class="group-card__badges">
            <!-- 还没扫通：卡上说的是扫描本身，不是「0 个地址」 -->
            <ScanBadge
              v-if="group.scan !== 'ok'"
              :state="group.scan"
              :error="group.scanError"
              :busy="busy"
              @cancel="emit('cancel-scan', group.id)"
              @retry="emit('retry-scan', group.id)"
            />
            <template v-else>
              <span class="badge">{{ t('check.group.count', { count: group.list.length }) }}</span>
              <span v-if="group.ok > 0" class="badge badge--ok">
                {{ t('check.group.ok', { count: group.ok }) }}
              </span>
              <span v-if="group.pending > 0" class="badge">
                {{ t('check.group.pending', { count: group.pending }) }}
              </span>
              <span v-if="group.bad > 0" class="badge badge--bad">
                {{ t('check.group.bad', { count: group.bad }) }}
              </span>
              <span v-if="group.unsupported > 0" class="badge badge--warn">
                {{ t('check.group.unsupported', { count: group.unsupported }) }}
              </span>
            </template>
          </span>
          <span
            v-if="group.list.length > 0"
            class="group-card__more"
            :class="{ 'group-card__more--on': isOpen(group.id) }"
          >
            {{ t(isOpen(group.id) ? 'row.details.collapse' : 'row.details.expand') }}
            <AppIcon class="group-card__chev" name="chevron-down" :size="13" />
          </span>
          <!-- 打开、定位、移除：和上传页的卡头同一组，不必先展开才够得着 -->
          <span class="group-card__tools" @keydown.stop>
            <button
              type="button"
              class="icon-btn icon-btn--sm"
              :title="t('row.open')"
              :aria-label="t('row.open')"
              @click.stop="openFile(group.path)"
            >
              <AppIcon name="external" :size="13" />
            </button>
            <button
              type="button"
              class="icon-btn icon-btn--sm"
              :title="t('row.reveal')"
              :aria-label="t('row.reveal')"
              @click.stop="emit('reveal', group.path)"
            >
              <AppIcon name="folder-open" :size="13" />
            </button>
            <button
              type="button"
              class="icon-btn icon-btn--sm icon-btn--drop"
              :title="t('row.remove')"
              :aria-label="t('row.remove')"
              :disabled="busy"
              @click.stop="emit('remove', group.id)"
            >
              <AppIcon name="x" :size="13" />
            </button>
          </span>
        </header>
        <div class="group-card__fold" :class="{ 'group-card__fold--open': isOpen(group.id) }">
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
        </div>
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
