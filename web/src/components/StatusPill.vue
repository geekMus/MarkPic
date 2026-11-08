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
/* 读数而不是徽章：圆点带颜色，文字保持安静，省掉一圈边框噪声 */
.pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--ink-3);
  font-size: var(--fs-data);
  font-weight: 500;
  white-space: nowrap;
}

.pill--compact {
  gap: 5px;
  font-size: var(--fs-label);
}

.pill__dot {
  width: 6px;
  height: 6px;
  flex: none;
  border-radius: 50%;
  background: currentColor;
}

.pill__dot--pulse {
  animation: pill-pulse 1.4s var(--ease) infinite;
}

.pill__label {
  line-height: 1;
}

.pill[data-tone='accent'] {
  color: var(--accent-ink);
}

.pill[data-tone='success'] {
  color: var(--ok);
}

.pill[data-tone='warning'] {
  color: var(--warn);
}

.pill[data-tone='danger'] {
  color: var(--bad);
}

@keyframes pill-pulse {
  0%,
  100% {
    opacity: 1;
  }
  50% {
    opacity: 0.3;
  }
}
</style>
