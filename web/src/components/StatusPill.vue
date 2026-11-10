<script setup lang="ts">
type PillTone = 'neutral' | 'accent' | 'success' | 'warning' | 'danger'

withDefaults(
  defineProps<{
    label: string
    tone?: PillTone
    dot?: boolean
    pulse?: boolean
    compact?: boolean
  }>(),
  { tone: 'neutral', dot: true, pulse: false, compact: false },
)
</script>

<template>
  <span class="pill" :class="{ 'pill--compact': compact }" :data-tone="tone">
    <span v-if="dot" class="pill__dot" :class="{ 'pill__dot--pulse': pulse }" aria-hidden="true" />
    <span class="pill__label">{{ label }}</span>
  </span>
</template>

<style scoped>
/* 读数而不是徽章：一个点 + 一句话，颜色只带在点上 */
.pill {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  padding: 3px 9px 3px 8px;
  border: 1px solid transparent;
  border-radius: 999px;
  color: var(--ink-2);
  font-size: var(--fs-2);
  font-weight: 500;
  white-space: nowrap;
  transition: background var(--t-fast) var(--ease), border-color var(--t-fast) var(--ease);
}

/* 有结论的行给一层很淡的底：状态是「读到的结果」，薄底让它从正文里浮出来一点，
   又不至于变成彩色标签墙。neutral（还在跑/没开始）保持透明。 */
.pill[data-tone='accent'] {
  border-color: var(--accent-line);
  background: color-mix(in srgb, var(--accent-soft) 72%, transparent);
}

.pill[data-tone='success'] {
  border-color: color-mix(in srgb, var(--ok) 26%, transparent);
  background: color-mix(in srgb, var(--ok-soft) 60%, transparent);
}

.pill[data-tone='warning'] {
  border-color: color-mix(in srgb, var(--warn) 28%, transparent);
  background: color-mix(in srgb, var(--warn-soft) 58%, transparent);
}

.pill[data-tone='danger'] {
  border-color: color-mix(in srgb, var(--bad) 30%, transparent);
  background: color-mix(in srgb, var(--bad-soft) 62%, transparent);
}

.pill--compact {
  gap: 6px;
  font-size: var(--fs-2);
}

.pill__dot {
  flex: none;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--ink-3);
  box-shadow: 0 0 0 3px rgba(138, 148, 163, 0.14);
}

.pill[data-tone='accent'] .pill__dot {
  background: var(--accent-vivid);
  box-shadow: 0 0 0 3px var(--accent-soft);
}

.pill[data-tone='success'] .pill__dot {
  background: var(--ok);
  box-shadow: 0 0 0 3px var(--ok-soft);
}

.pill[data-tone='warning'] .pill__dot {
  background: var(--warn);
  box-shadow: 0 0 0 3px var(--warn-soft);
}

.pill[data-tone='danger'] .pill__dot {
  background: var(--bad);
  box-shadow: 0 0 0 3px var(--bad-soft);
}

.pill[data-tone='accent'] .pill__label {
  color: var(--accent-ink);
}

.pill[data-tone='success'] .pill__label {
  color: var(--ok);
}

.pill[data-tone='warning'] .pill__label {
  color: var(--warn);
}

.pill[data-tone='danger'] .pill__label {
  color: var(--bad);
}

.pill__dot--pulse {
  animation: pulse 1.6s var(--ease) infinite;
}

@keyframes pulse {
  0%,
  100% {
    opacity: 1;
  }

  50% {
    opacity: 0.35;
  }
}
</style>
