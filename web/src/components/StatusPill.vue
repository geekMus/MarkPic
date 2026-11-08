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
.pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 22px;
  padding: 0 9px;
  border: 1px solid var(--border);
  border-radius: var(--r-pill);
  background: var(--surface-2);
  color: var(--text-2);
  font-size: 11.5px;
  font-weight: 500;
  white-space: nowrap;
}

.pill--compact {
  height: 20px;
  padding: 0 8px;
  font-size: 11px;
  gap: 5px;
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

.pill[data-tone='neutral'] {
  color: var(--text-2);
}

.pill[data-tone='neutral'] .pill__dot {
  background: var(--text-3);
}

.pill[data-tone='accent'] {
  border-color: var(--accent-border);
  background: var(--accent-soft);
  color: var(--accent-text);
}

.pill[data-tone='success'] {
  border-color: var(--success-border);
  background: var(--success-soft);
  color: var(--success);
}

.pill[data-tone='warning'] {
  border-color: var(--warning-border);
  background: var(--warning-soft);
  color: var(--warning);
}

.pill[data-tone='danger'] {
  border-color: var(--danger-border);
  background: var(--danger-soft);
  color: var(--danger);
}

@keyframes pill-pulse {
  0%,
  100% {
    opacity: 1;
  }
  50% {
    opacity: 0.35;
  }
}
</style>
