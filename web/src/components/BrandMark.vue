<script setup lang="ts">
import { computed } from 'vue'

/**
 * 品牌标记：一块圆角方片 + 一根圆头笔画写出的 m + 右上角一颗圆点，跟着主题换配色。
 *
 * 必须内联成 SVG，不能外链：窗口用 file:// 加载，不额外请求任何文件。
 * 几何与 assets/mark-{light,dark}.svg 逐字相同 —— 改形状时那两份 SVG、这里的路径、
 * 以及 web/index.html 的 favicon 要一起改（favicon 由 scripts/make-favicon.py 生成）。
 */

type Stop = readonly [color: string, offset: number, opacity?: number]

type Palette = {
  tile: readonly Stop[]
  ridge: readonly Stop[]
  glow: readonly Stop[]
  dot: string
  border: string
  borderOpacity: number
}

// 暗色：夜蓝方片，笔画从深青扫到薄荷白，圆点取暖橙
const DARK: Palette = {
  tile: [
    ['#263750', 0],
    ['#101826', 0.52],
    ['#080d16', 1],
  ],
  ridge: [
    ['#1f8fa6', 0],
    ['#5fd9c9', 0.52],
    ['#e9fffb', 1],
  ],
  glow: [
    ['#ffd9a8', 0, 0.14],
    ['#5fd9c9', 0.55, 0.07],
    ['#5fd9c9', 1, 0],
  ],
  dot: '#ffd9a8',
  border: '#ffffff',
  borderOpacity: 0.08,
}

// 亮色：方片退成一层薄荷白的纸，笔画压深成深青→薄荷绿，圆点压成琥珀（白底上才看得见）
const LIGHT: Palette = {
  tile: [
    ['#f4fcfa', 0],
    ['#e6f5f2', 0.52],
    ['#cfe9e4', 1],
  ],
  ridge: [
    ['#0b5d6b', 0],
    ['#0f7f78', 0.52],
    ['#129a89', 1],
  ],
  glow: [
    ['#ffffff', 0, 0.8],
    ['#ffffff', 0.55, 0.32],
    ['#ffffff', 1, 0],
  ],
  dot: '#d4891f',
  border: '#0b5d6b',
  borderOpacity: 0.14,
}

const props = defineProps<{ theme: 'light' | 'dark' }>()

const palette = computed(() => (props.theme === 'dark' ? DARK : LIGHT))
</script>

<template>
  <svg viewBox="0 0 64 64" fill="none" aria-hidden="true">
    <defs>
      <linearGradient id="mpmark-tile" x1="14" y1="2" x2="50" y2="62" gradientUnits="userSpaceOnUse">
        <stop
          v-for="([color, offset]) in palette.tile"
          :key="`tile-${offset}`"
          :stop-color="color"
          :offset="offset"
        />
      </linearGradient>
      <linearGradient id="mpmark-ridge" x1="15" y1="46" x2="51" y2="20" gradientUnits="userSpaceOnUse">
        <stop
          v-for="([color, offset]) in palette.ridge"
          :key="`ridge-${offset}`"
          :stop-color="color"
          :offset="offset"
        />
      </linearGradient>
      <radialGradient id="mpmark-glow" cx="46.6" cy="14.4" r="26" gradientUnits="userSpaceOnUse">
        <stop
          v-for="([color, offset, opacity]) in palette.glow"
          :key="`glow-${offset}`"
          :stop-color="color"
          :offset="offset"
          :stop-opacity="opacity"
        />
      </radialGradient>
    </defs>

    <rect x="2" y="2" width="60" height="60" rx="15.5" fill="url(#mpmark-tile)" />
    <rect x="2" y="2" width="60" height="60" rx="15.5" fill="url(#mpmark-glow)" />
    <circle cx="46.6" cy="14.4" r="3.2" :fill="palette.dot" />
    <path
      d="M15 46V30.6C15 25.5 18.9 20.4 24 20.4C29.1 20.4 32.5 25.5 32.5 30.6V46M32.5 30.6C32.5 25.6 36.4 21.6 41.2 21.6C46 21.6 50 25.6 50 30.6V46"
      stroke="url(#mpmark-ridge)"
      stroke-width="5"
      stroke-linecap="round"
      stroke-linejoin="round"
    />
    <!-- 内描边：亮色里方片的边界全靠这一圈，因为方片本身只是白纸 -->
    <rect
      x="2.5"
      y="2.5"
      width="59"
      height="59"
      rx="15"
      :stroke="palette.border"
      :stroke-opacity="palette.borderOpacity"
    />
  </svg>
</template>

<style scoped>
svg {
  display: block;
  width: 100%;
  height: 100%;
}
</style>