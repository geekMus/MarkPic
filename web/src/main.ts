import { createApp } from 'vue'
import App from './App.vue'
import './assets/main.css'
import { watchViewportResize } from './composables/useResizing'

// 拉窗口 / 拖窗口边缘时把最贵的视觉效果临时关掉，缩放才跟手
watchViewportResize()

// 桌面窗口里没有「网页缩放」这回事：Ctrl+滚轮 / Ctrl+加减号会把界面整体放大
// 而窗口大小不变，版面立刻错位（右侧、底部留出空白）。
// 这里把浏览器那套缩放入口关掉，窗口该多大就多大。
window.addEventListener(
  'wheel',
  (event) => {
    if (event.ctrlKey) {
      event.preventDefault()
    }
  },
  { passive: false },
)

window.addEventListener('keydown', (event) => {
  if (!event.ctrlKey && !event.metaKey) {
    return
  }
  if (['+', '=', '-', '_', '0'].includes(event.key)) {
    event.preventDefault()
  }
})

createApp(App).mount('#app')
