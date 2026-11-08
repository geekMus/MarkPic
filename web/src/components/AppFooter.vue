<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import type { RunPhase, RunStats } from '../composables/useRun'

const props = defineProps<{
  stats: RunStats
  phase: RunPhase
  busy: boolean
  canStart: boolean
  canReveal: boolean
  dryRun: boolean
}>()

const emit = defineEmits<{
  run: []
  cancel: []
  reveal: []
}>()

type Part = { text: string; tone: 'plain' | 'warn' | 'bad' }

const settled = computed(
  () =>
    props.phase === 'running' ||
    props.phase === 'cancelling' ||
    props.phase === 'done' ||
    props.phase === 'cancelled',
)

/** 状态栏读数：文件数、引用数，跑完之后追加替换与失败计数。 */
const parts = computed<Part[]>(() => {
  const stats = props.stats
  if (stats.files === 0) return [{ text: '队列为空', tone: 'plain' }]

  const list: Part[] = [{ text: `${stats.files} 个文件`, tone: 'plain' }]
  list.push(
    stats.images > 0
      ? { text: `${stats.images} 张图`, tone: 'plain' }
      : { text: '未发现图片', tone: 'plain' },
  )
  if (settled.value) {
    list.push({ text: `替换 ${stats.uploaded}`, tone: 'plain' })
    if (stats.failed > 0) list.push({ text: `失败 ${stats.failed}`, tone: 'bad' })
  }
  if (props.phase === 'cancelled') list.push({ text: '已取消', tone: 'warn' })
  return list
})

const showReveal = computed(
  () => props.canReveal && (props.phase === 'done' || props.phase === 'cancelled'),
)

const showShortcut = computed(() => props.canStart && !props.busy)
</script>

<template>
  <footer class="footer">
    <div class="footer__readout" role="status" aria-live="polite">
      <template v-for="(part, i) in parts" :key="part.text">
        <span v-if="i > 0" class="footer__sep" aria-hidden="true">·</span>
        <span class="footer__part num" :data-tone="part.tone">{{ part.text }}</span>
      </template>
      <span v-if="dryRun" class="tag tag--warn footer__dry">试运行</span>
    </div>

    <div class="footer__actions">
      <span v-if="showShortcut" class="footer__kbd">
        <span class="kbd">Ctrl</span>
        <span class="kbd">Enter</span>
      </span>

      <button
        v-if="showReveal"
        type="button"
        class="btn btn--secondary btn--sm"
        @click="emit('reveal')"
      >
        <AppIcon name="folder-open" :size="14" />
        打开输出目录
      </button>

      <button v-if="phase === 'preparing'" type="button" class="btn btn--primary" disabled>
        启动中
      </button>
      <button v-else-if="phase === 'cancelling'" type="button" class="btn btn--danger" disabled>
        正在取消
      </button>
      <button v-else-if="busy" type="button" class="btn btn--danger" @click="emit('cancel')">
        取消
      </button>
      <button
        v-else
        type="button"
        class="btn btn--primary"
        :disabled="!canStart"
        :title="canStart ? '开始转换' : '请先加入文件'"
        @click="emit('run')"
      >
        开始转换
      </button>
    </div>
  </footer>
</template>

<style scoped>
.footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  height: 44px;
  padding: 0 16px;
  border-top: 1px solid var(--line);
  background: var(--surface);
  z-index: 20;
}

.footer__readout {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.footer__part {
  color: var(--ink-2);
  font-size: var(--fs-data);
  white-space: nowrap;
}

.footer__sep {
  color: var(--line-2);
}

.footer__part[data-tone='bad'] {
  color: var(--bad);
}

.footer__part[data-tone='warn'] {
  color: var(--warn);
}

.footer__dry {
  flex: none;
}

.footer__kbd {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  margin-right: 4px;
  color: var(--ink-3);
  font-size: var(--fs-label);
}

.footer__actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: none;
}

@media (max-width: 720px) {
  .footer__kbd {
    display: none;
  }
}
</style>
