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
    <svg class="empty__art" viewBox="0 0 176 136" fill="none" aria-hidden="true">
      <rect x="24" y="14" width="88" height="108" rx="10" stroke="var(--border-strong)" stroke-width="1.5" />
      <path d="M90 14v20h22" stroke="var(--border-strong)" stroke-width="1.5" stroke-linejoin="round" />
      <path
        d="M40 44h44M40 56h28"
        stroke="var(--text-3)"
        stroke-width="2"
        stroke-linecap="round"
        opacity="0.85"
      />
      <rect x="40" y="68" width="56" height="34" rx="5" stroke="var(--text-3)" stroke-width="1.5" />
      <path
        d="M44 96l12-14 9 9 7-6 11 11"
        stroke="var(--text-3)"
        stroke-width="1.6"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
      <path
        d="M73 77.4a2.6 2.6 0 1 0 0 5.2 2.6 2.6 0 0 0 0-5.2"
        stroke="var(--text-3)"
        stroke-width="1.6"
      />
      <path d="M116 74h14" stroke="var(--accent)" stroke-width="2" stroke-linecap="round" />
      <path
        d="M125 66l8 8-8 8"
        stroke="var(--accent)"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
      <circle cx="150" cy="74" r="14" stroke="var(--accent)" stroke-width="1.6" opacity="0.9" />
      <ellipse cx="150" cy="74" rx="6" ry="14" stroke="var(--accent)" stroke-width="1.3" opacity="0.6" />
      <path d="M136 74h28" stroke="var(--accent)" stroke-width="1.3" opacity="0.6" />
    </svg>

    <h2 class="empty__title">把本地图片换成图床链接</h2>
    <p class="empty__text">
      读取 Markdown 中引用的本地图片，经由本机 PicGo 上传到图床，再把文档里的链接替换为图床地址并另存。
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
  max-width: 460px;
  padding: 28px 24px;
  text-align: center;
}

.empty__art {
  width: 176px;
  height: 136px;
  margin-bottom: 18px;
}

.empty__title {
  color: var(--text-1);
  font-size: 15px;
  font-weight: 600;
  line-height: 1.4;
}

.empty__text {
  max-width: 384px;
  margin-top: 8px;
  color: var(--text-2);
  font-size: 12.5px;
  line-height: 1.65;
}

.empty__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 8px;
  margin-top: 18px;
}

.empty__hint {
  margin-top: 12px;
  color: var(--text-3);
  font-size: 11.5px;
}
</style>
