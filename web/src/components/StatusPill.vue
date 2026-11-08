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
  color: var(--ink-2);
  font-size: var(--fs-2);
  font-weight: 500;
  white-space: nowrap;
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
