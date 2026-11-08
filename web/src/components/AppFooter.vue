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

// 试运行没有真正写出文件，别给一个必然失败的「打开输出目录」
const showReveal = computed(
  () =>
    props.canReveal && !props.dryRun && (props.phase === 'done' || props.phase === 'cancelled'),
)

const showShortcut = computed(() => props.canStart && !props.busy)
</script>

<template>
  <footer class="foot">
    <div class="foot__stats" role="status" aria-live="polite">
      <template v-for="(part, index) in parts" :key="index">
        <span v-if="index > 0" class="foot__sep" aria-hidden="true">·</span>
        <span class="foot__part" :data-tone="part.tone">{{ part.text }}</span>
      </template>
      <span v-if="dryRun" class="tag tag--warn">试运行</span>
    </div>

    <div class="foot__actions">
      <span v-if="showShortcut" class="foot__kbd" aria-hidden="true">
        <kbd class="kbd">Ctrl</kbd>
        <span class="foot__plus">+</span>
        <kbd class="kbd">Enter</kbd>
      </span>

      <button v-if="showReveal" type="button" class="btn btn--secondary" @click="emit('reveal')">
        <AppIcon name="folder-open" :size="15" />
        打开输出目录
      </button>

      <button
        v-if="phase === 'preparing'"
        type="button"
        class="btn btn--primary btn--lg"
        disabled
      >
        启动中
      </button>
      <button
        v-else-if="phase === 'cancelling'"
        type="button"
        class="btn btn--secondary btn--lg"
        disabled
      >
        正在取消
      </button>
      <button
        v-else-if="busy"
        type="button"
        class="btn btn--secondary btn--lg"
        @click="emit('cancel')"
      >
        取消
      </button>
      <button
        v-else
        type="button"
        class="btn btn--primary btn--lg"
        :disabled="!canStart"
        @click="emit('run')"
      >
        <AppIcon name="play" :size="15" />
        开始转换
      </button>
    </div>
  </footer>
</template>

<style scoped>
.foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  min-width: 0;
}

.foot__stats {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  min-width: 0;
  color: var(--ink-2);
  font-size: var(--fs-2);
}

.foot__part {
  color: var(--ink-2);
  white-space: nowrap;
}

.foot__part[data-tone='bad'] {
  color: var(--bad);
  font-weight: 600;
}

.foot__part[data-tone='warn'] {
  color: var(--warn);
}

.foot__sep {
  color: var(--line-3);
}

.foot__actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex: none;
}

.foot__kbd {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: var(--ink-3);
  font-size: var(--fs-1);
}

.foot__plus {
  color: var(--ink-3);
}

@media (max-width: 760px) {
  .foot__kbd {
    display: none;
  }
}
</style>
