<script setup lang="ts">
import { computed } from 'vue'
import { useI18n, type MessageKey } from '../i18n'
import type { JobView } from '../types'

/**
 * 空态的「配图」：一段真实的 Markdown，一个地址被划掉、换成另一个。
 *
 * 不用抽象插画 —— 这个工具的工作就是这一处替换，把它画出来比任何示意图都
 * 更能说明问题。方向随菜单变：转换是把本地图传到图床，下载是把网上的图收回
 * 本地，检测则根本不改文档，只在每条网络地址后面标一个结论。
 *
 * 颜色语义固定，不随方向翻转：琥珀 = 还在本机，青 = 已经在图床。所以转换里
 * 被划掉的是琥珀、新地址是青；下载正好反过来，被划掉的是青、新地址是琥珀。
 *
 * 动效只有一次：进页面时划线从左往右走，新地址随后浮出来，然后彻底安静。
 * prefers-reduced-motion 下直接呈现终态。
 */
const props = defineProps<{ mode: JobView }>()

const { t } = useI18n()

const LOCAL = 'images/arch.png'
const REMOTE = 'https://pic.example.com/arch.webp'

/** 检测不改文档，只给结论，所以整段换成「地址 + 结论」。 */
const isCheck = computed(() => props.mode === 'check')

/** download 方向相反：被换掉的是远程地址，换上的是本地路径。 */
const reversed = computed(() => props.mode === 'download')

const wasText = computed(() => (reversed.value ? REMOTE : LOCAL))
const nowText = computed(() => (reversed.value ? LOCAL : REMOTE))

const wasTone = computed(() => (reversed.value ? 'doc__addr--remote' : 'doc__addr--local'))
const nowTone = computed(() => (reversed.value ? 'doc__addr--local' : 'doc__addr--remote'))

const stateKey = computed<MessageKey>(() => {
  if (isCheck.value) return 'empty.specimen.state.check'
  if (reversed.value) return 'empty.specimen.state.download'
  return 'empty.specimen.state'
})

const captionKey = computed<MessageKey>(() => {
  if (isCheck.value) return 'empty.specimen.caption.check'
  if (reversed.value) return 'empty.specimen.caption.download'
  return 'empty.specimen.caption'
})
</script>

<template>
  <figure class="specimen">
    <div class="doc">
      <div class="doc__bar">
        <span class="doc__name mono">{{ t('empty.specimen.file') }}</span>
        <span class="doc__state">{{ t(stateKey) }}</span>
      </div>

      <div class="doc__body mono">
        <p class="doc__h">
          <span class="tok-dim">#</span>
          {{ t('empty.specimen.heading') }}
        </p>

        <!-- 检测：不改文档，每条网络地址后面直接跟结论 -->
        <template v-if="isCheck">
          <p class="doc__line">
            <span class="tok-dim">![</span><span class="tok-alt">{{ t('empty.specimen.alt') }}</span
            ><span class="tok-dim">](</span><span class="doc__addr--remote">https://pic.example.com/arch.webp</span
            ><span class="tok-dim">)</span>
            <span class="pill pill--ok">{{ t('empty.specimen.ok') }}</span>
          </p>
          <p class="doc__line">
            <span class="tok-dim">![</span><span class="tok-alt">{{ t('empty.specimen.alt2') }}</span
            ><span class="tok-dim">](</span><span class="doc__addr--dead">https://cdn.example.org/legacy/logo.png</span
            ><span class="tok-dim">)</span>
            <span class="pill pill--dead">{{ t('empty.specimen.dead') }}</span>
          </p>
        </template>

        <!-- 转换 / 下载：一处替换，旧地址划掉，新地址浮出来 -->
        <template v-else>
          <p class="doc__line">
            <span class="tok-dim">![</span><span class="tok-alt">{{ t('empty.specimen.alt') }}</span
            ><span class="tok-dim">](</span
            ><span class="doc__addr doc__addr--strike" :class="wasTone">{{ wasText }}</span
            ><span class="tok-dim">)</span>
          </p>

          <p class="doc__line doc__line--now">
            <span class="now__arrow" aria-hidden="true">↳</span>
            <span class="tok-dim">![</span><span class="tok-alt">{{ t('empty.specimen.alt') }}</span
            ><span class="tok-dim">](</span
            ><span class="doc__addr" :class="nowTone">{{ nowText }}</span
            ><span class="tok-dim">)</span>
          </p>

          <!-- 文档里那张图：换地址前后都是它。alt 写的是架构图，那就画一张架构图。 -->
          <svg class="doc__pic" viewBox="0 0 240 96" role="img" :aria-label="t('empty.specimen.alt')">
            <rect width="240" height="96" rx="6" fill="var(--panel-3)" />
            <g stroke="var(--accent)" stroke-opacity="0.45" stroke-width="1.4" fill="none">
              <path d="M72 48 H88" />
              <path d="M152 48 H168" />
            </g>
            <g fill="var(--accent)" fill-opacity="0.45">
              <path d="M88 44.5 L93 48 L88 51.5 Z" />
              <path d="M168 44.5 L173 48 L168 51.5 Z" />
            </g>
            <g>
              <rect
                x="18"
                y="34"
                width="54"
                height="28"
                rx="5"
                fill="var(--accent)"
                fill-opacity="0.14"
                stroke="var(--accent)"
                stroke-opacity="0.4"
              />
              <rect
                x="93"
                y="30"
                width="59"
                height="36"
                rx="5"
                fill="var(--accent)"
                fill-opacity="0.3"
                stroke="var(--accent)"
                stroke-opacity="0.7"
              />
              <rect
                x="173"
                y="34"
                width="49"
                height="28"
                rx="5"
                fill="var(--accent)"
                fill-opacity="0.14"
                stroke="var(--accent)"
                stroke-opacity="0.4"
              />
            </g>
            <g fill="var(--ink-3)" fill-opacity="0.55">
              <rect x="27" y="43" width="30" height="3" rx="1.5" />
              <rect x="27" y="50" width="19" height="3" rx="1.5" />
              <rect x="182" y="43" width="28" height="3" rx="1.5" />
              <rect x="182" y="50" width="17" height="3" rx="1.5" />
            </g>
            <g fill="var(--accent-ink)">
              <rect x="103" y="40" width="39" height="3.5" rx="1.75" />
              <rect x="103" y="48" width="26" height="3.5" rx="1.75" />
            </g>
            <circle cx="206" cy="20" r="4" fill="var(--warn)" fill-opacity="0.7" />
          </svg>
        </template>
      </div>
    </div>

    <figcaption class="specimen__cap">{{ t(captionKey) }}</figcaption>
  </figure>
</template>

<style scoped>
.specimen {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin: 0;
}

/* 一张纸，不是一个「功能卡片」：只有这一处配得上投影。 */
.doc {
  overflow: hidden;
  border: 1px solid var(--line-2);
  border-radius: var(--r-4);
  background: var(--panel-2);
  box-shadow: var(--shadow-panel);
}

.doc__bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 9px 14px;
  border-bottom: 1px solid var(--line);
}

.doc__name {
  color: var(--ink-2);
  font-size: var(--fs-1);
}

.doc__state {
  color: var(--ink-3);
  font-size: 10.5px;
}

.doc__body {
  padding: 16px 16px 18px;
  font-size: var(--fs-2);
  line-height: 1.85;
}

.doc__h {
  margin: 0 0 12px;
  color: var(--ink);
  font-weight: 600;
}

.doc__line {
  margin: 0;
  overflow-wrap: anywhere;
}

.tok-dim {
  color: var(--ink-3);
}

.tok-alt {
  color: var(--ink-2);
}

/* 两个地址色：琥珀 = 还在本机，青 = 已经在图床。方向变了，颜色含义不变。 */
.doc__addr--local {
  color: var(--warn);
}

.doc__addr--remote {
  color: var(--accent-ink);
}

.doc__addr--dead {
  color: var(--bad);
}

/* 被换掉的那一行：划线从左往右走一次。不再整行压透明度 ——
   删除线本身已经说明「这条被换掉了」，再叠 0.62 会让浅色主题下的
   琥珀色掉到 2.5:1，读不清。 */
.doc__addr--strike {
  position: relative;
}

.doc__addr--strike::after {
  position: absolute;
  top: 52%;
  right: 0;
  left: 0;
  height: 1px;
  background: currentColor;
  content: '';
  transform-origin: left center;
  animation: strike 340ms var(--ease) 420ms both;
}

.doc__line--now {
  display: block;
  animation: settle 400ms var(--ease) 760ms both;
}

.now__arrow {
  margin-right: 6px;
  color: var(--accent);
}

/* 检测结论：跟在地址后面的一小片，不是新的一行 */
.pill {
  display: inline-block;
  margin-left: 8px;
  padding: 1px 7px;
  border: 1px solid transparent;
  border-radius: 999px;
  font-size: 10.5px;
  font-family: var(--font);
  white-space: nowrap;
}

.pill--ok {
  border-color: var(--ok-line);
  background: var(--ok-soft);
  color: var(--ok);
}

.pill--dead {
  border-color: var(--bad-line);
  background: var(--bad-soft);
  color: var(--bad);
}

.doc__pic {
  display: block;
  width: 100%;
  height: auto;
  margin-top: 14px;
  border-radius: var(--r-2);
}

.specimen__cap {
  color: var(--ink-3);
  font-size: var(--fs-1);
  line-height: 1.6;
}

@keyframes strike {
  from {
    transform: scaleX(0);
  }
  to {
    transform: scaleX(1);
  }
}

@keyframes settle {
  from {
    opacity: 0;
    transform: translateY(6px);
  }
  to {
    opacity: 1;
    transform: none;
  }
}

@media (prefers-reduced-motion: reduce) {
  .doc__addr--strike::after,
  .doc__line--now {
    animation: none;
  }
}
</style>