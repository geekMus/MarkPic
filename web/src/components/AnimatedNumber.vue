<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'

/**
 * 会「滚」的数字：值变了就缓一下滚过去，不是啪地跳上去。
 *
 * 只在值变化时动 —— 页面刚出现时直接显示终值，不让人等它数完。
 * 缓动取三次方 ease-out，与全局 --ease 的观感一致（先快后慢、收得稳）。
 */
const props = withDefaults(defineProps<{ value: number; duration?: number }>(), { duration: 460 })

const shown = ref(props.value)
const reduced = window.matchMedia('(prefers-reduced-motion: reduce)')
let raf = 0

function stop(): void {
  if (raf !== 0) {
    cancelAnimationFrame(raf)
    raf = 0
  }
}

function tween(from: number, to: number): void {
  stop()
  if (reduced.matches || from === to) {
    shown.value = to
    return
  }
  const start = performance.now()
  const tick = (now: number): void => {
    const k = Math.min(1, (now - start) / props.duration)
    const eased = 1 - (1 - k) ** 3
    shown.value = Math.round(from + (to - from) * eased)
    if (k < 1) raf = requestAnimationFrame(tick)
    else shown.value = to
  }
  raf = requestAnimationFrame(tick)
}

watch(
  () => props.value,
  (to, from) => tween(from ?? to, to),
)

onBeforeUnmount(stop)
</script>

<template>
  <span class="num">{{ shown }}</span>
</template>
