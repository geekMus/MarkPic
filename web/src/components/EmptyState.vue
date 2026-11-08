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
    <!-- 发丝线技术插图：左侧是文档里的本地引用，右侧是替换后的图床地址 -->
    <svg class="empty__art" viewBox="0 0 168 116" fill="none" aria-hidden="true">
      <path
        d="M22.5 8.5h62l14 14v85a3 3 0 0 1-3 3h-73a3 3 0 0 1-3-3v-96a3 3 0 0 1 3-3z"
        stroke="var(--line-2)"
        stroke-width="1.4"
      />
      <path d="M84.5 8.5v14h14" stroke="var(--line-2)" stroke-width="1.4" stroke-linejoin="round" />

      <path
        d="M32 40h44M32 50h30"
        stroke="var(--ink-3)"
        stroke-width="2"
        stroke-linecap="round"
        opacity="0.5"
      />

      <rect x="32" y="62" width="44" height="30" rx="3" stroke="var(--ink-3)" stroke-width="1.3" />
      <path
        d="M35 87l10-12 7.5 7.5 6-5 8.5 9.5"
        stroke="var(--ink-3)"
        stroke-width="1.3"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
      <circle cx="66.5" cy="70" r="2.6" stroke="var(--ink-3)" stroke-width="1.3" />

      <path
        d="M108 34h18"
        stroke="var(--line-2)"
        stroke-width="1.4"
        stroke-linecap="round"
        stroke-dasharray="3 3"
      />
      <path
        d="M121.5 29.5l5 4.5-5 4.5"
        stroke="var(--accent)"
        stroke-width="1.6"
        stroke-linecap="round"
        stroke-linejoin="round"
      />

      <rect
        x="112"
        y="52"
        width="44"
        height="30"
        rx="4"
        stroke="var(--accent)"
        stroke-width="1.4"
        opacity="0.9"
      />
      <path
        d="M126 70.5a4.5 4.5 0 0 0 6.4.4l2.6-2.6a4.5 4.5 0 0 0-6.4-6.4l-1.5 1.5"
        stroke="var(--accent)"
        stroke-width="1.3"
        stroke-linecap="round"
      />
      <path
        d="M141 63.5a4.5 4.5 0 0 0-6.4-.4l-2.6 2.6a4.5 4.5 0 0 0 6.4 6.4l1.5-1.5"
        stroke="var(--accent)"
        stroke-width="1.3"
        stroke-linecap="round"
      />
    </svg>

    <h2 class="empty__title">把本地图片换成图床链接</h2>
    <p class="empty__text">
      读取 Markdown 里引用的本地图片，经本机 PicGo 上传到图床，再把文档中的链接替换为图床地址并另存。
    </p>

    <div class="empty__actions">
      <button type="button" class="btn btn--primary" @click="emit('pick-files')">
        <AppIcon name="file-plus" :size="15" />
        选择文件
      </button>
      <button type="button" class="btn btn--secondary" @click="emit('pick-folder')">
        <AppIcon name="folder-plus" :size="15" />
        选择文件夹
      </button>
      <button type="button" class="btn btn--ghost" @click="emit('manual')">
        <AppIcon name="terminal" :size="15" />
        手动输入路径
      </button>
    </div>

    <p class="empty__hint">支持 Windows 绝对路径，可一次加入多个文件</p>
  </div>
</template>

<style scoped>
.empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  margin: auto;
  max-width: 440px;
  padding: 32px 24px;
  text-align: center;
}

.empty__art {
  width: 168px;
  height: 116px;
  margin-bottom: 20px;
}

.empty__title {
  color: var(--ink);
  font-size: var(--fs-lead);
  font-weight: 600;
  line-height: 1.4;
}

.empty__text {
  max-width: 380px;
  margin-top: 8px;
  color: var(--ink-2);
  font-size: var(--fs-body);
  line-height: 1.7;
}

.empty__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 8px;
  margin-top: 20px;
}

.empty__hint {
  margin-top: 14px;
  color: var(--ink-3);
  font-size: var(--fs-label);
}
</style>
