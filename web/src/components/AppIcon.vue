<script setup lang="ts">
import { computed } from 'vue'

type IconName =
  | 'file-plus'
  | 'folder-plus'
  | 'folder-open'
  | 'terminal'
  | 'trash'
  | 'chevron-down'
  | 'sun'
  | 'moon'
  | 'monitor'
  | 'settings'
  | 'x'
  | 'refresh'
  | 'alert'
  | 'info'
  | 'check'
  | 'play'
  | 'image'
  | 'link'

const props = withDefaults(defineProps<{ name: IconName; size?: number }>(), { size: 16 })

const ICONS: Record<IconName, string[]> = {
  'file-plus': [
    'M14.5 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7.5z',
    'M14.5 3v4.5H19',
    'M12 12.5v5',
    'M9.5 15h5',
  ],
  'folder-plus': [
    'M3 8.6V6.5a2 2 0 0 1 2-2h3.4a2 2 0 0 1 1.4.6l1 1a2 2 0 0 0 1.4.6H19a2 2 0 0 1 2 2v9.8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z',
    'M12 12.4v5.2',
    'M9.4 15h5.2',
  ],
  'folder-open': [
    'M3 9V6.5a2 2 0 0 1 2-2h3.4a2 2 0 0 1 1.4.6l1 1a2 2 0 0 0 1.4.6H18a2 2 0 0 1 2 2v1.6',
    'M3.7 10.3h16.6a1.4 1.4 0 0 1 1.36 1.73l-1.4 5.6A2 2 0 0 1 18.33 19H5.67a2 2 0 0 1-1.93-1.37l-1.4-5.6A1.4 1.4 0 0 1 3.7 10.3z',
  ],
  terminal: ['M5.5 7.5l4.5 4.5-4.5 4.5', 'M13 16.5h5.5'],
  trash: [
    'M4.5 7h15',
    'M9.7 7V5.6A1.6 1.6 0 0 1 11.3 4h1.4a1.6 1.6 0 0 1 1.6 1.6V7',
    'M6.7 7l.76 11.1A2 2 0 0 0 9.46 20h5.08a2 2 0 0 0 2-1.9L17.3 7',
    'M10.4 11v5.4',
    'M13.6 11v5.4',
  ],
  'chevron-down': ['M6.5 9.5l5.5 5.5 5.5-5.5'],
  sun: [
    'M12 7.6a4.4 4.4 0 1 0 0 8.8 4.4 4.4 0 0 0 0-8.8',
    'M12 2.4v2.2',
    'M12 19.4v2.2',
    'M2.4 12h2.2',
    'M19.4 12h2.2',
    'M5.2 5.2l1.56 1.56',
    'M17.24 17.24l1.56 1.56',
    'M18.8 5.2l-1.56 1.56',
    'M6.76 17.24L5.2 18.8',
  ],
  moon: ['M20.2 14.4A8.6 8.6 0 0 1 9.6 3.8a8.6 8.6 0 1 0 10.6 10.6z'],
  /* 跟随系统时用显示器，和太阳 / 月亮区分开 */
  monitor: [
    'M3.4 5.4h17.2a1.4 1.4 0 0 1 1.4 1.4v8.4a1.4 1.4 0 0 1-1.4 1.4H3.4A1.4 1.4 0 0 1 2 15.2V6.8a1.4 1.4 0 0 1 1.4-1.4z',
    'M8.6 20.6h6.8',
    'M12 16.6v4',
  ],
  settings: [
    'M4 7h9',
    'M15 5.4a1.6 1.6 0 1 0 0 3.2 1.6 1.6 0 0 0 0-3.2',
    'M18.2 7H20',
    'M4 12h3',
    'M9 10.4a1.6 1.6 0 1 0 0 3.2 1.6 1.6 0 0 0 0-3.2',
    'M12.2 12H20',
    'M4 17h7',
    'M13 15.4a1.6 1.6 0 1 0 0 3.2 1.6 1.6 0 0 0 0-3.2',
    'M16.2 17H20',
  ],
  x: ['M6.2 6.2l11.6 11.6', 'M17.8 6.2L6.2 17.8'],
  refresh: [
    'M20.4 12a8.4 8.4 0 0 1-14.4 5.85',
    'M3.6 12a8.4 8.4 0 0 1 14.4-5.85',
    'M18.4 3.4V7H14.8',
    'M5.6 20.6V17h3.6',
  ],
  alert: ['M12 4.2l8.8 15.3H3.2z', 'M12 10v4.2', 'M12 17.1h.01'],
  info: ['M12 3.6a8.4 8.4 0 1 0 0 16.8 8.4 8.4 0 0 0 0-16.8', 'M12 11v5.4', 'M12 7.9h.01'],
  check: ['M5.2 12.7l4.3 4.3L18.8 7.7'],
  play: ['M8.4 5.6l10.4 6.4-10.4 6.4z'],
  image: [
    'M4.6 5.4h14.8a1.2 1.2 0 0 1 1.2 1.2v10.8a1.2 1.2 0 0 1-1.2 1.2H4.6a1.2 1.2 0 0 1-1.2-1.2V6.6a1.2 1.2 0 0 1 1.2-1.2z',
    'M3.8 16.2l4.6-4.6 3.4 3.4 3-3 4.4 4.4',
    'M9 9.6h.01',
  ],
  link: [
    'M10.2 13.6a4.4 4.4 0 0 0 6.24.36l2.5-2.5a4.4 4.4 0 0 0-6.22-6.22l-1.44 1.44',
    'M13.8 10.4a4.4 4.4 0 0 0-6.24-.36l-2.5 2.5a4.4 4.4 0 0 0 6.22 6.22l1.44-1.44',
  ],
}

const paths = computed<string[]>(() => ICONS[props.name])
</script>

<template>
  <svg
    class="icon"
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.7"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
    focusable="false"
  >
    <path v-for="(d, index) in paths" :key="index" :d="d" />
  </svg>
</template>

<style scoped>
.icon {
  flex: none;
}
</style>
