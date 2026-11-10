<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import StatusPill from './StatusPill.vue'
import type { RunPhase, RunRowView, RunStats } from '../composables/useRun'
import { useI18n, type MessageKey } from '../i18n'

const props = defineProps<{
  stats: RunStats
  phase: RunPhase
  /** 队列里被选中的那一行；没选就是 null。 */
  row: RunRowView | null
  completed: number
  total: number
  endpoint: string
  picgoOnline: boolean
  picgoNeeded: boolean
}>()

const emit = defineEmits<{
  reveal: [path: string]
  remove: [id: string]
}>()

const { t } = useI18n()

// 右栏只报「这一轮做了什么」，用的是与页脚同一批数字；9 个指标全列会变成噪音，
// 所以只列非零项，一轮跑完是什么都没发生就只剩文件与图片两个数。
const metrics = computed<{ key: MessageKey; value: number }[]>(() => {
  const s = props.stats
  const out: { key: MessageKey; value: number }[] = []
  const push = (key: MessageKey, n: number): void => {
    if (n > 0) out.push({ key, value: n })
  }
  push('inspector.files', s.files)
  push('inspector.images', s.images)
  push('inspector.replaced', s.uploaded)
  push('inspector.reused', s.reused)
  push('inspector.downloaded', s.localizedUnique > 0 ? s.localizedUnique : s.localized)
  push('inspector.checked', s.remoteCheckedUnique > 0 ? s.remoteCheckedUnique : s.remoteChecked)
  push('inspector.dead', s.remoteDeadUnique > 0 ? s.remoteDeadUnique : s.remoteDead)
  push('inspector.kept', s.remoteKept)
  push('inspector.localMissing', s.localMissing)
  push('inspector.failed', s.failed)
  return out
})

const overall = computed(() => {
  if (props.total <= 0) return 0
  const done = props.phase === 'done' || props.phase === 'cancelled' ? props.total : props.completed
  return Math.min(100, Math.round((done / props.total) * 100))
})

const STATUS_LABEL: Record<string, MessageKey> = {
  ok: 'row.status.done',
  partial: 'row.status.partial',
  'no-images': 'row.status.noImages',
  failed: 'row.status.failed',
  cancelled: 'row.status.cancelled',
  preview: 'row.status.preview',
  checked: 'row.status.checked',
}

const statusOf = computed(() => {
  const res = props.row?.result
  if (!res) return null
  const key = STATUS_LABEL[res.status] ?? 'row.status.queued'
  const tone =
    res.status === 'failed'
      ? ('danger' as const)
      : res.status === 'partial'
        ? ('warning' as const)
        : res.status === 'checked' || res.status === 'preview'
          ? ('accent' as const)
          : ('success' as const)
  return { key, tone }
})

const notes = computed<string[]>(() => {
  const res = props.row?.result
  if (!res) return []
  const out: string[] = []
  if (res.error) out.push(res.error)
  // 计数类的问题也要说出来：只检测/只下载的轮次里，一篇文档「没有 error」不等于没有问题。
  const dead = (res.dead ?? 0) + (res.remoteFailed ?? 0)
  if (dead > 0) out.push(t('row.reason.dead', { count: dead }))
  const missingLocals = res.localMissing ?? 0
  if (missingLocals > 0) out.push(t('row.reason.localMissing', { count: missingLocals }))
  const kept = res.remoteKept ?? 0
  if (kept > 0) out.push(t('row.reason.remoteKept', { count: kept }))
  for (const w of res.warnings ?? []) out.push(w)
  return [...new Set(out)].slice(0, 4)
})

const imageCount = computed(() => props.row?.images ?? 0)
const moved = computed(() => {
  const r = props.row
  if (!r) return 0
  const total = r.images ?? 0
  if (total <= 0) return 0
  const done = r.done ?? r.uploaded
  return Math.min(100, Math.round((done / total) * 100))
})
const outputOf = computed(() => props.row?.result?.outputPath ?? '')
</script>

<template>
  <aside class="side" :aria-label="t('inspector.label')">
    <section class="card">
      <header class="card__head">
        <h2 class="card__title">{{ t('inspector.run') }}</h2>
        <span class="card__pct num">{{ overall }}%</span>
      </header>
      <div class="bar" aria-hidden="true">
        <span :style="{ width: overall + '%' }" />
      </div>
      <dl class="metrics">
        <template v-for="m in metrics" :key="m.key">
          <dt>{{ t(m.key) }}</dt>
          <dd class="num" :data-zero="m.value === 0 ? 'yes' : null">{{ m.value }}</dd>
        </template>
      </dl>
      <p v-if="metrics.length === 0" class="hint">{{ t('inspector.idle') }}</p>
    </section>

    <section class="card card--grow">
      <header class="card__head">
        <h2 class="card__title">{{ t('inspector.selected') }}</h2>
        <StatusPill v-if="statusOf" :label="t(statusOf.key)" :tone="statusOf.tone" compact />
      </header>

      <template v-if="row">
        <p class="name" :title="row.path">{{ row.name }}</p>
        <p class="dir mono" :title="row.path">{{ row.dir }}</p>

        <div class="facts">
          <span class="facts__item">
            <AppIcon name="image" :size="13" />
            {{ imageCount }}
          </span>
          <span v-if="outputOf" class="facts__item facts__item--ok" :title="outputOf">
            <AppIcon name="check" :size="13" />
            {{ t('inspector.hasOutput') }}
          </span>
        </div>

        <div v-if="row.phase === 'running'" class="bar bar--thin" aria-hidden="true">
          <span :style="{ width: moved + '%' }" />
        </div>

        <ul v-if="notes.length > 0" class="notes">
          <li v-for="(note, i) in notes" :key="i" class="notes__item" :title="note">{{ note }}</li>
        </ul>
        <p v-else-if="row.result" class="hint">{{ t('inspector.noProblems') }}</p>

        <div class="actions">
          <button type="button" class="btn btn--ghost btn--sm" @click="emit('reveal', outputOf || row.path)">
            <AppIcon name="folder-open" :size="14" />
            {{ t('inspector.reveal') }}
          </button>
          <button
            type="button"
            class="btn btn--ghost btn--sm btn--danger"
            @click="emit('remove', row.id)"
          >
            <AppIcon name="trash" :size="14" />
            {{ t('inspector.remove') }}
          </button>
        </div>
      </template>

      <p v-else class="hint hint--center">{{ t('inspector.empty') }}</p>
    </section>

    <section class="card card--tight">
      <header class="card__head">
        <h2 class="card__title">{{ t('inspector.picgo') }}</h2>
        <span class="dot" :data-on="picgoOnline ? 'yes' : null" aria-hidden="true" />
      </header>
      <p class="mono endpoint" :title="endpoint">{{ endpoint }}</p>
      <p class="hint">
        {{ picgoNeeded ? t(picgoOnline ? 'header.picgo.online' : 'header.picgo.offline') : t('header.picgo.notNeeded') }}
      </p>
    </section>
  </aside>
</template>

<style scoped>
.side {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-height: 0;
  overflow: auto;
}

.card {
  display: flex;
  flex-direction: column;
  gap: 9px;
  padding: 13px 14px 14px;
  border: 1px solid var(--border-panel);
  border-radius: var(--r-5);
  background: linear-gradient(180deg, color-mix(in srgb, var(--panel) 88%, white 12%), var(--panel) 120px);
  box-shadow: var(--shadow-panel);
}

.card--grow {
  min-height: 168px;
}

.card--tight {
  gap: 6px;
}

.card__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.card__title {
  font-size: var(--fs-2);
  font-weight: 600;
  letter-spacing: 0.03em;
  color: var(--ink-2);
}

.card__pct {
  font-size: var(--fs-1);
  color: var(--ink-3);
}

.bar {
  height: 4px;
  border-radius: 999px;
  background: color-mix(in srgb, var(--line-2) 70%, transparent);
  overflow: hidden;
}

.bar > span {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: linear-gradient(90deg, var(--accent), var(--accent-vivid));
  transition: width var(--t-base) var(--ease);
}

.bar--thin {
  height: 3px;
}

.metrics {
  display: grid;
  grid-template-columns: 1fr auto;
  gap: 5px 10px;
  margin: 0;
}

.metrics dt {
  color: var(--ink-3);
  font-size: var(--fs-1);
}

.metrics dd {
  margin: 0;
  color: var(--ink);
  font-size: var(--fs-2);
  font-weight: 600;
  text-align: right;
}

.name {
  font-size: var(--fs-3);
  font-weight: 600;
  color: var(--ink);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dir {
  font-size: var(--fs-1);
  color: var(--ink-3);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.facts {
  display: flex;
  align-items: center;
  gap: 10px;
  color: var(--ink-3);
  font-size: var(--fs-1);
}

.facts__item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.facts__item--ok {
  color: var(--ok);
}

.notes {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.notes__item {
  padding: 5px 8px;
  border-radius: 8px;
  background: color-mix(in srgb, var(--bad-soft) 52%, transparent);
  color: var(--ink-2);
  font-size: var(--fs-1);
  line-height: 1.45;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.hint {
  margin: 0;
  color: var(--ink-3);
  font-size: var(--fs-1);
  line-height: 1.5;
}

.hint--center {
  padding: 22px 0;
  text-align: center;
}

.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 2px;
}

.endpoint {
  font-size: var(--fs-1);
  color: var(--ink-2);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dot {
  width: 7px;
  height: 7px;
  border-radius: 999px;
  background: var(--ink-3);
  opacity: 0.5;
}

.dot[data-on='yes'] {
  background: var(--ok);
  opacity: 1;
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--ok) 18%, transparent);
}
</style>