import { computed, nextTick, ref, type ShallowRef } from 'vue'

/**
 * 逐图明细的虚拟滚动。
 *
 * 一篇两万张图的文档，展开就是两万行：实测 184164 个 DOM 节点、95 MB 堆、
 * 展开要 2.06 秒，而用户真正能看见的永远只有那十几行。超过阈值就只渲染
 * 可视区附近的行，用上下两个占位块撑出滚动高度。
 *
 * 明细行高度是固定的（单行 + 省略号，实测 30px），所以不需要像文件列表
 * 那样按行 id 记实测高度 —— 那是另一个更复杂的算法，别把两者合并。
 */

/** 超过这个条数就收进定高的小窗口：一百张图的文档不能让卡片撑成三千像素。 */
const LIST_LIMIT = 12
/** 超过这个条数才启用虚拟滚动：几十行全渲染反而更简单，也不会有滚动条。 */
const VIRTUAL_THRESHOLD = 60
/** 明细行高度（`.items__row` 的 min-height 30px，单行不换行）。 */
const ITEM_HEIGHT = 30
/** 可视区上下各多渲染几行，滚动时不至于露白。 */
const ITEM_OVERSCAN = 8
/** 虚拟滚动时明细区的最大高度：再高就把整页滚动条抢走了。 */
export const ITEM_VIEWPORT = 420

/**
 * 明细区的窗口计算。
 *
 * `itemsEl` 由调用方用模板引用（`useTemplateRef`）持有 —— 展开时要把滚动位置拨回去，
 * 得拿到真实的 DOM 节点，所以这个 ref 不从内部新建。
 */
export function useItemWindow<T>(
  items: { readonly value: T[] },
  itemsEl: Readonly<ShallowRef<HTMLElement | null>>,
) {
  const itemsScrollTop = ref(0)
  const itemsViewport = ref(ITEM_VIEWPORT)

  const bounded = computed(() => items.value.length > LIST_LIMIT)
  const virtualItems = computed(() => items.value.length > VIRTUAL_THRESHOLD)

  const itemWindow = computed(() => {
    const total = items.value.length
    if (!virtualItems.value) {
      return { start: 0, end: total, padTop: 0, padBottom: 0 }
    }
    const height = itemsViewport.value > 0 ? itemsViewport.value : ITEM_VIEWPORT
    const visible = Math.ceil(height / ITEM_HEIGHT) + ITEM_OVERSCAN * 2
    let start = Math.floor(itemsScrollTop.value / ITEM_HEIGHT) - ITEM_OVERSCAN
    if (start < 0) start = 0
    let end = start + visible
    if (end > total) end = total
    return { start, end, padTop: start * ITEM_HEIGHT, padBottom: (total - end) * ITEM_HEIGHT }
  })

  const visibleItems = computed(() => items.value.slice(itemWindow.value.start, itemWindow.value.end))

  function onScroll(event: Event): void {
    const el = event.target as HTMLElement
    itemsScrollTop.value = el.scrollTop
    if (el.clientHeight > 0) itemsViewport.value = el.clientHeight
  }

  /** 刚展开时把明细区拨回顶部：收起来前停在中段的列表，再展开不该还停在那儿。 */
  function resetScroll(): void {
    itemsScrollTop.value = 0
    void nextTick(() => {
      const el = itemsEl.value
      if (!el) return
      el.scrollTop = 0
      if (el.clientHeight > 0) itemsViewport.value = el.clientHeight
    })
  }

  return { itemsScrollTop, itemsViewport, bounded, virtualItems, itemWindow, visibleItems, onScroll, resetScroll }
}
