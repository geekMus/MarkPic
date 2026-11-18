<script setup lang="ts">
import { computed } from 'vue'
import { openTarget } from '../api'

/**
 * 把发布说明当「文字」渲染：标题、列表、段落、加粗、行内代码、链接。
 *
 * 结构照 magpie 的 noteBlocks()：发布说明只有标题和短列表，没必要为它背一个
 * Markdown 库，切块 + 行内标记就够；正文先转义再加标记，所以它进 v-html 也
 * 不会把提交信息里的尖括号变成标签。
 */
const props = defineProps<{ markdown: string }>()

type Block =
  | { kind: 'heading'; level: number; html: string }
  | { kind: 'list'; items: string[] }
  | { kind: 'para'; html: string }

/** 这几节是给「下载安装」看的，程序自己就是安装器，对话框里不需要。 */
const SKIP = new Set(['安装', '安装包', 'install', 'installation'])

const ESCAPES: Record<string, string> = {
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
  "'": '&#39;',
}

/** 行内标记 → HTML。转义在前，标记在后。 */
function inline(text: string): string {
  let out = text.replace(/[&<>"']/gu, (ch) => ESCAPES[ch] ?? ch)
  out = out.replace(/`([^`]+)`/gu, '<code>$1</code>')
  out = out.replace(/\*\*([^*]+)\*\*/gu, '<strong>$1</strong>')
  out = out.replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/gu, '<a href="$2" rel="noreferrer noopener">$1</a>')
  out = out.replace(/(^|[\s(（])(https?:\/\/[^\s<)）]+)/gu, '$1<a href="$2" rel="noreferrer noopener">$2</a>')
  return out
}

/** 比较标题用的纯文本：去掉强调符号和小写，认「安装 / Install」两种写法。 */
function plain(text: string): string {
  return text.replace(/[*_`~]/gu, '').trim().toLowerCase()
}

const blocks = computed<Block[]>(() => {
  const out: Block[] = []
  let list: string[] | null = null
  let skipLevel = 0

  for (const raw of props.markdown.split(/\r?\n/u)) {
    const line = raw.replace(/\s+$/u, '')
    const heading = /^ {0,3}(#{1,6})\s+(.*?)\s*#*\s*$/u.exec(line)
    if (heading) {
      const level = heading[1].length
      const text = heading[2].trim()
      list = null
      if (SKIP.has(plain(text))) {
        skipLevel = level
        continue
      }
      if (skipLevel > 0) {
        if (level <= skipLevel) skipLevel = 0
        else continue
      }
      // 版本号那一行由对话框头部显示，正文里不再重复一遍
      if (/^v?\d{4}\.\d{2}\.\d{2}/u.test(text)) continue
      out.push({ kind: 'heading', level, html: inline(text) })
      continue
    }
    if (skipLevel > 0) continue

    const item = /^\s*[-*+]\s+(.*)$/u.exec(line)
    if (item) {
      if (!list) {
        list = []
        out.push({ kind: 'list', items: list })
      }
      list.push(inline(item[1]))
      continue
    }
    if (!line.trim()) {
      list = null
      continue
    }
    // 缩进的一行是上一条的续行（提交正文里的换行）
    if (list && /^\s{2,}\S/u.test(line)) {
      list[list.length - 1] += ` ${inline(line.trim())}`
      continue
    }
    list = null
    out.push({ kind: 'para', html: inline(line.trim()) })
  }
  return out
})

/** 链接一律交给系统浏览器，别让 WebView 自己导航走。 */
function onClick(event: MouseEvent): void {
  const anchor = (event.target as HTMLElement | null)?.closest('a')
  if (!anchor) return
  event.preventDefault()
  const href = anchor.getAttribute('href') ?? ''
  if (!/^https?:\/\//iu.test(href)) return
  void openTarget(href).catch(() => {
    // 浏览器打不开就算了：链接文字本身还看得见
  })
}
</script>

<template>
  <div class="notes selectable" @click="onClick">
    <template v-for="(block, index) in blocks" :key="index">
      <h4
        v-if="block.kind === 'heading'"
        class="notes__h"
        :data-level="Math.min(block.level, 4)"
        v-html="block.html"
      />
      <ul v-else-if="block.kind === 'list'" class="notes__list">
        <li v-for="(item, i) in block.items" :key="i" v-html="item" />
      </ul>
      <p v-else class="notes__p" v-html="block.html" />
    </template>
  </div>
</template>

<style scoped>
.notes {
  color: var(--ink-2);
  font-size: var(--fs-2);
  line-height: 1.6;
}

.notes__h {
  margin: 16px 0 6px;
  color: var(--ink);
  font-size: var(--fs-2);
  font-weight: 600;
}

.notes > :first-child {
  margin-top: 0;
}

.notes__list {
  margin: 0 0 10px;
  padding-left: 20px;
  /* 全局把列表圆点清掉了（导航自己画点），这里得还回来：否则三条改进会读成
     一段缩进的正文。 */
  list-style: disc outside;
}

/* 不用 grid 排版：display 一改，列表项就不画圆点了，只能看着像缩进的段落。 */
.notes__list li + li {
  margin-top: 4px;
}

.notes__list li::marker {
  color: var(--ink-3);
}

.notes__p {
  margin: 0 0 10px;
}

.notes :deep(code) {
  padding: 1px 4px;
  border-radius: 4px;
  background: var(--track);
  font-size: 0.95em;
}

.notes :deep(a) {
  color: var(--accent-ink);
  text-decoration: underline dotted;
  text-underline-offset: 2px;
}
</style>
