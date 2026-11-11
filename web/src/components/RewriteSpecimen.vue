<script setup lang="ts">
import { useI18n } from '../i18n'

/**
 * 空态的「配图」：一段真实的 Markdown，本地路径被划掉、换成图床链接。
 *
 * 不用抽象插画 —— 这个工具的全部工作就是这一处替换，把它画出来比任何
 * 示意图都更能说明问题。文档里那张图片也一并画出来（就是应用图标里的那座山），
 * 让人一眼看出「换的是地址，图还是那张图」。
 *
 * 动效只有一次：进页面时划线从左往右走，新地址随后浮出来，然后彻底安静。
 * prefers-reduced-motion 下直接呈现终态。
 */
const { t } = useI18n()
</script>

<template>
  <figure class="specimen">
    <div class="doc">
      <div class="doc__bar">
        <span class="doc__name mono">{{ t('empty.specimen.file') }}</span>
        <span class="doc__state">{{ t('empty.specimen.state') }}</span>
      </div>

      <div class="doc__body mono">
        <p class="doc__h">
          <span class="tok-dim">#</span>
          {{ t('empty.specimen.heading') }}
        </p>

        <p class="doc__line doc__line--was">
          <span class="tok-dim">![</span><span class="tok-alt">{{ t('empty.specimen.alt') }}</span
          ><span class="tok-dim">](</span><span class="was">images/arch.png</span
          ><span class="tok-dim">)</span>
        </p>

        <p class="doc__line doc__line--now">
          <span class="now__arrow" aria-hidden="true">↳</span>
          <span class="tok-dim">![</span><span class="tok-alt">{{ t('empty.specimen.alt') }}</span
          ><span class="tok-dim">](</span><span class="now">https://pic.example.com/arch.webp</span
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
      </div>
    </div>

    <figcaption class="specimen__cap">{{ t('empty.specimen.caption') }}</figcaption>
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

/* 旧地址：琥珀色 = 还在本机、马上要被换掉 */
.was {
  position: relative;
  color: var(--warn);
}

.was::after {
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

/* 不再整行压透明度：删除线本身已经说明「这条被换掉了」，
   再叠 0.62 会让浅色主题下的琥珀色掉到 2.5:1，读不清。 */

/* 新地址：青色 = 已经在图床上 */
.doc__line--now {
  display: block;
  animation: settle 400ms var(--ease) 760ms both;
}

.now {
  color: var(--accent-ink);
}

.now__arrow {
  margin-right: 6px;
  color: var(--accent);
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
  .was::after,
  .doc__line--now {
    animation: none;
  }
}
</style>