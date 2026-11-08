<script setup lang="ts">
import AppIcon from './AppIcon.vue'

const emit = defineEmits<{
  'pick-files': []
  'pick-folder': []
  manual: []
}>()
</script>

<template>
  <div class="empty">
    <div class="empty__inner">
      <div class="empty__hero">
        <div class="empty__copy">
          <h2 class="empty__title">把本地图片换成图床链接</h2>
          <p class="empty__text">
            读取 Markdown 里引用的本地图片，经本机 PicGo 上传，再把文档里的地址替换成图床链接。
            默认另存一份副本，原文件不动。
          </p>

          <div class="empty__actions">
            <button type="button" class="btn btn--primary btn--lg" @click="emit('pick-files')">
              <AppIcon name="file-plus" :size="15" />
              选择文件
            </button>
            <button type="button" class="btn btn--secondary btn--lg" @click="emit('pick-folder')">
              <AppIcon name="folder-plus" :size="15" />
              选择文件夹
            </button>
            <button type="button" class="btn btn--ghost btn--lg" @click="emit('manual')">
              <AppIcon name="terminal" :size="15" />
              手动输入路径
            </button>
          </div>

          <p class="empty__foot">
            可以一次加入多个文件或整个目录，也可以直接把 Windows 绝对路径粘进来。
          </p>
        </div>

        <!-- 发丝线插图：左边文档里框着本地图片，右边是上传后的图床 -->
        <div class="empty__art" aria-hidden="true">
          <svg viewBox="0 0 260 190" fill="none">
            <rect x="16" y="24" width="104" height="140" rx="14" fill="var(--panel)" stroke="var(--line-2)" />
            <path d="M92 24v18a6 6 0 0 0 6 6h18" stroke="var(--line-2)" stroke-linejoin="round" />
            <rect x="32" y="70" width="54" height="7" rx="3.5" fill="var(--line-2)" />
            <rect x="32" y="85" width="34" height="7" rx="3.5" fill="var(--line)" />

            <rect
              x="32"
              y="104"
              width="72"
              height="44"
              rx="9"
              fill="var(--accent-soft)"
              stroke="var(--accent-line)"
            />
            <path
              d="M40 141l12-13.5 8 8.5 7-6 9 11"
              stroke="var(--accent)"
              stroke-width="1.5"
              stroke-linecap="round"
              stroke-linejoin="round"
            />
            <circle cx="91" cy="115" r="3.2" stroke="var(--accent)" stroke-width="1.5" />

            <path
              d="M126 106q22-1 34-16"
              stroke="var(--accent)"
              stroke-width="1.5"
              stroke-linecap="round"
              stroke-dasharray="4 5"
            />
            <path d="M152 86l7 6-8.5 2.4z" fill="var(--accent)" />

            <circle cx="196" cy="82" r="47" fill="var(--accent-soft)" opacity="0.72" />
            <rect
              x="164"
              y="50"
              width="64"
              height="64"
              rx="18"
              fill="var(--panel)"
              stroke="var(--accent-line)"
            />
            <path
              d="M180.5 92.5a8 8 0 0 1 0.6-15.9 11.5 11.5 0 0 1 21.8 2.4 7 7 0 0 1 1.6 13.5z"
              stroke="var(--accent)"
              stroke-width="1.5"
              stroke-linejoin="round"
            />
            <path
              d="M196 88V72m0 0-6 6m6-6 6 6"
              stroke="var(--accent)"
              stroke-width="1.5"
              stroke-linecap="round"
              stroke-linejoin="round"
            />

            <rect x="169" y="126" width="54" height="6" rx="3" fill="var(--line-2)" />
            <rect x="173" y="139" width="42" height="6" rx="3" fill="var(--line)" />
            <rect x="177" y="152" width="28" height="6" rx="3" fill="var(--line)" />
          </svg>
        </div>
      </div>

      <!-- 支持的写法：第一次用的人最想知道「我这份文档能不能被认出来」 -->
      <section class="spec">
        <h3 class="spec__title">支持的写法</h3>
        <ul class="spec__list">
          <li class="spec__item mono">![说明](images/a.png)</li>
          <li class="spec__item mono">![](<带 空格.png>)</li>
          <li class="spec__item mono">&lt;img src="a.png" width="60"&gt;</li>
        </ul>
        <p class="spec__note">
          围栏代码块与行内代码里的图片会被跳过；已经是网络地址的图片原样保留。
        </p>
      </section>

      <ol class="steps">
        <li class="step">
          <span class="step__n num">1</span>
          <span class="step__body">
            <span class="step__t">加入 Markdown</span>
            <span class="step__d">文件、文件夹或直接粘路径</span>
          </span>
        </li>
        <li class="step">
          <span class="step__n num">2</span>
          <span class="step__body">
            <span class="step__t">交给本机 PicGo 上传</span>
            <span class="step__d">同一张图一轮只传一次</span>
          </span>
        </li>
        <li class="step">
          <span class="step__n num">3</span>
          <span class="step__body">
            <span class="step__t">回写链接并另存</span>
            <span class="step__d">失败项保持原文，不串位</span>
          </span>
        </li>
      </ol>
    </div>
  </div>
</template>

<style scoped>
/* 空态贴着面板顶部，步骤条钉在底部：中间那片留白是刻意的，不是没对齐 */
.empty {
  display: flex;
  height: 100%;
  padding: 42px 44px 30px;
}

.empty__inner {
  display: flex;
  flex-direction: column;
  gap: 26px;
  width: 100%;
  max-width: 900px;
  margin: 0 auto;
}

.empty__hero {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: 48px;
}

.empty__copy {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 12px;
  min-width: 0;
}

.empty__title {
  color: var(--ink);
  font-size: 25px;
  font-weight: 600;
  letter-spacing: -0.015em;
  line-height: 1.3;
}

.empty__text {
  max-width: 44ch;
  color: var(--ink-2);
  font-size: var(--fs-3);
  line-height: 1.7;
}

.empty__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin-top: 6px;
}

.empty__foot {
  color: var(--ink-3);
  font-size: var(--fs-1);
}

.empty__art {
  flex: none;
  width: 304px;
  fill: none;
}

.empty__art svg {
  display: block;
  width: 100%;
  height: auto;
}

/* 支持的写法：把中间那片留白换成第一次用的人真正会问的东西 */
.spec {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 16px 18px;
  border: 1px solid var(--line);
  border-radius: var(--r-3);
  background: var(--panel-2);
}

.spec__title {
  color: var(--ink-3);
  font-size: var(--fs-1);
  font-weight: 600;
  letter-spacing: 0.01em;
}

.spec__list {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.spec__item {
  padding: 5px 9px;
  border: 1px solid var(--line-2);
  border-radius: var(--r-1);
  background: var(--panel);
  color: var(--ink-2);
  font-size: var(--fs-2);
}

.spec__note {
  color: var(--ink-3);
  font-size: var(--fs-1);
  line-height: 1.6;
}

/* 三步说明：把「空」变成「知道下一步做什么」 */
.steps {
  display: grid;
  margin-top: auto;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 20px;
  padding-top: 22px;
  border-top: 1px solid var(--line);
}

.step {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  min-width: 0;
}

.step__n {
  display: grid;
  place-items: center;
  flex: none;
  width: 20px;
  height: 20px;
  margin-top: 1px;
  border: 1px solid var(--accent-line);
  border-radius: 50%;
  background: var(--accent-soft);
  color: var(--accent-ink);
  font-size: 11px;
  font-weight: 600;
}

.step__body {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.step__t {
  color: var(--ink);
  font-size: var(--fs-2);
  font-weight: 600;
}

.step__d {
  color: var(--ink-3);
  font-size: var(--fs-1);
  line-height: 1.5;
}

/* 默认窗口宽 1180：够宽就左右两栏，窄下来再竖排 */
@media (max-width: 1040px) {
  .empty__hero {
    grid-template-columns: minmax(0, 1fr);
    gap: 20px;
  }

  .empty__art {
    display: none;
  }
}

/* 窄窗口（820 以下就是窄版）：压缩纵向节奏，让整块内容一屏放得下 */
@media (max-width: 900px) {
  .empty {
    padding: 22px 20px 18px;
  }

  .empty__inner {
    gap: 16px;
  }

  .empty__hero {
    gap: 16px;
  }

  .empty__title {
    font-size: 21px;
  }

  .empty__text {
    line-height: 1.6;
  }

  .spec {
    padding: 12px 14px;
  }

  .steps {
    gap: 14px;
    padding-top: 12px;
  }

  .step {
    gap: 8px;
  }
}

/* 再窄：三步改成竖排，一列一行 */
@media (max-width: 620px) {
  .steps {
    grid-template-columns: minmax(0, 1fr);
    gap: 10px;
  }
}
</style>
