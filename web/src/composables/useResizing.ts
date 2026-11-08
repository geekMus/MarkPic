/**
 * 拖动窗口边缘时，浏览器每一帧都要重排、重绘整个界面。
 * 平时看着好看的几样东西这时候最贵：固定定位的径向渐变要整屏重画、
 * 阴影和毛玻璃要重新合成、过渡动画还会和重排抢时间。
 * 拖拽期间把它们关掉，松手安静 180ms 后再恢复 —— 用户看到的是一次
 * 丝滑的缩放，而不是一场特效演示。
 */
let timer = 0

/** 标记「正在缩放窗口」，重复调用只重置计时。 */
export function markResizing(): void {
  const root = document.documentElement
  if (root.dataset.resizing === undefined) {
    root.dataset.resizing = ''
  }
  window.clearTimeout(timer)
  // 系统缩放循环结束（鼠标松开）之后不会再有任何事件，只能靠安静时长判断
  timer = window.setTimeout(() => {
    delete root.dataset.resizing
  }, 180)
}

/** 监听视口变化：桌面窗口里由拖拽触发，浏览器里由拉窗口触发。 */
export function watchViewportResize(): void {
  window.addEventListener('resize', markResizing, { passive: true })
}
