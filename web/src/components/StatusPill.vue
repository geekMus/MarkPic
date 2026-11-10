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
/* 读数而不是徽章：一个小圆点 + 一句话。颜色只带在点和字上，
   不铺底色、不描边 —— 一屏二十行的时候，彩色标签墙比信息更吵。 */
.pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 0;
  color: var(--ink-3);
  font-size: var(--fs-1);
  font-weight: 500;
  white-space: nowrap;
}

.pill--compact {
  gap: 6px;
  font-size: var(--fs-1);
}

.pill__dot {
  flex: none;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--ink-3);
}

.pill[data-tone='accent'] .pill__dot {
  background: var(--accent-vivid);
}

.pill[data-tone='success'] .pill__dot {
  background: var(--ok);
}

.pill[data-tone='warning'] .pill__dot {
  background: var(--warn);
}

.pill[data-tone='danger'] .pill__dot {
  background: var(--bad);
}

.pill[data-tone='accent'] .pill__label {
  color: var(--accent-ink);
}

.pill[data-tone='success'] .pill__label {
  color: var(--ink-2);
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