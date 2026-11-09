<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import type { RunPhase, RunStats } from '../composables/useRun'
import { useI18n } from '../i18n'

const props = defineProps<{
  stats: RunStats
  phase: RunPhase
  busy: boolean
  canStart: boolean
  canReveal: boolean
  dryRun: boolean
  /** 服务端报上来的版本串，例如 1.0.0 (b7fb0d2)。 */
  version: string
  /** 已经查到有新版本时，在版本号旁边点一个记号。 */
  hasUpdate: boolean
}>()

const emit = defineEmits<{
  run: []
  cancel: []
  reveal: []
  about: []
}>()

const { t } = useI18n()

/** 底栏只放 v1.0.0 这样的短号，提交号留给关于对话框。 */
const versionShort = computed(() => {
  const raw = props.version.trim()
  if (raw === '') return '—'
  return `v${raw.split(/[\s(]/)[0]}`
})

type Part = { text: string; tone: 'plain' | 'warn' | 'bad' }

const settled = computed(
  () =>
    props.phase === 'running' ||
    props.phase === 'cancelling' ||
    props.phase === 'done' ||
    props.phase === 'cancelled',
)

/** 状态栏读数：文件数、引用数，跑完之后追加替换与失败计数。 */
const parts = computed<Part[]>(() => {
  const stats = props.stats
  if (stats.files === 0) return [{ text: t('footer.queueEmpty'), tone: 'plain' }]

  const list: Part[] = [{ text: t('footer.files', { count: stats.files }), tone: 'plain' }]
  list.push(
    stats.images > 0
      ? { text: t('footer.images', { count: stats.images }), tone: 'plain' }
      : { text: t('footer.noImages'), tone: 'plain' },
  )
  if (settled.value) {
    // 只碰远程图的两种模式里「替换 0」是句废话，把版面让给下载/检测那几个数。
    const remoteOnly = stats.localized > 0 || stats.remoteChecked > 0 || stats.filesChecked > 0
    if (stats.uploaded > 0 || !remoteOnly) {
      list.push({ text: t('footer.uploaded', { count: stats.uploaded }), tone: 'plain' })
    }
    if (stats.reused > 0) {
      list.push({ text: t('footer.reused', { count: stats.reused }), tone: 'plain' })
    }
    if (stats.remoteReused > 0) {
      list.push({ text: t('footer.remoteReused', { count: stats.remoteReused }), tone: 'plain' })
    }
    if (stats.failed > 0) {
      list.push({ text: t('footer.failed', { count: stats.failed }), tone: 'bad' })
    }
    if (stats.localized > 0) {
      // 同一张图被引用多次时 localized 是引用处数，可 images/ 里只有一个文件。
      // 只显示引用处数会让人以为其余的没下下来，所以去重后的张数才是主数。
      const unique = stats.localizedUnique > 0 ? stats.localizedUnique : stats.localized
      if (unique < stats.localized) {
        list.push({
          text: t('footer.localizedUnique', { count: unique, refs: stats.localized }),
          tone: 'plain',
        })
      } else {
        list.push({ text: t('footer.localized', { count: unique }), tone: 'plain' })
      }
    }
    if (stats.remoteChecked > 0 && stats.localized === 0) {
      // 检测模式：remoteChecked 就是处理过的远程引用总数（含失效的）。
      // 下载模式不能这么念 —— 那边只把「没下来的」记进 remoteChecked，
      // 说「检测 15 个远程地址 · 其中失效 15」会让人以为一张都没下来。
      // 同一张图被引用多次时，「检测 10 个地址」会显得我们敲了十次门；
      // 真正探过的地址数和引用处数差着说，才和命令行读起来一致。
      const checkedText =
        stats.remoteCheckedUnique > 0 && stats.remoteCheckedUnique < stats.remoteChecked
          ? t('footer.remoteCheckedRefs', {
              unique: stats.remoteCheckedUnique,
              count: stats.remoteChecked,
            })
          : t('footer.remoteChecked', { count: stats.remoteChecked })
      list.push({ text: checkedText, tone: 'plain' })
      if (stats.remoteDead > 0) {
        // 失效那一侧同样有「按地址算」和「按引用处算」两个数：一个死链被引用
        // 3 次时，只说「其中失效 3」会和结尾清单里的「1 个地址」打架。
        const deadText =
          stats.remoteDeadUnique > 0 && stats.remoteDeadUnique < stats.remoteDead
            ? t('footer.remoteDeadRefs', {
                unique: stats.remoteDeadUnique,
                count: stats.remoteDead,
              })
            : t('footer.remoteDead', { count: stats.remoteDead })
        list.push({ text: deadText, tone: 'bad' })
      }
    } else if (stats.remoteDead > 0 || stats.remoteFailed > 0) {
      // 下载模式：地址本身坏掉的、和这次没下来的，在用户眼里都是「没拿回来」。
      // 合成一句才和结尾那份清单（按地址去重）对得上；早先只算 dead，
      // 于是汇总说「1 个地址」、清单说「2 个」，自己跟自己打架。
      const total = stats.remoteDead + stats.remoteFailed
      const unique = stats.remoteDeadUnique + stats.remoteFailedUnique
      const deadText =
        unique > 0 && unique < total
          ? t('footer.remoteDeadDownloadedRefs', { unique, count: total })
          : t('footer.remoteDeadDownloaded', { count: total })
      list.push({ text: deadText, tone: 'bad' })
    } else if (stats.filesChecked > 0) {
      list.push({ text: t('footer.checked', { count: stats.filesChecked }), tone: 'plain' })
    }
    if (stats.localMissing > 0) {
      // 检测/下载模式都不碰本地图，缺图却是文档自己的毛病，页脚也得说一句。
      list.push({ text: t('footer.localMissing', { count: stats.localMissing }), tone: 'warn' })
    }
    if (stats.remoteUnsupported > 0) {
      // ftp://、data: 这类地址既不算检测过、也不算失效，不单列就没人知道它们存在。
      list.push({
        text: t('footer.remoteUnsupported', { count: stats.remoteUnsupported }),
        tone: 'warn',
      })
    }
  }
  if (props.phase === 'cancelled') list.push({ text: t('footer.cancelled'), tone: 'warn' })
  return list
})

// 试运行没有真正写出文件，别给一个必然失败的「打开输出目录」
const showReveal = computed(
  () =>
    props.canReveal && !props.dryRun && (props.phase === 'done' || props.phase === 'cancelled'),
)

const showShortcut = computed(() => props.canStart && !props.busy)
</script>

<template>
  <footer class="foot">
    <div class="foot__stats" role="status" aria-live="polite">
      <template v-for="(part, index) in parts" :key="index">
        <span v-if="index > 0" class="foot__sep" aria-hidden="true">·</span>
        <span class="foot__part" :data-tone="part.tone">{{ part.text }}</span>
      </template>
      <span v-if="dryRun" class="tag tag--warn">{{ t('footer.dryRun') }}</span>
    </div>

    <div class="foot__actions">
      <!-- 版本号放在页面底部的状态栏里，点开是「关于 / 更新」 -->
      <button
        type="button"
        class="foot__ver"
        :title="t('footer.version', { version })"
        @click="emit('about')"
      >
        <span class="mono">{{ versionShort }}</span>
        <span v-if="hasUpdate" class="foot__ver-dot" :aria-label="t('footer.updateAvailable')" />
      </button>

      <span v-if="showShortcut" class="foot__kbd" aria-hidden="true">
        <kbd class="kbd">Ctrl</kbd>
        <span class="foot__plus">+</span>
        <kbd class="kbd">Enter</kbd>
      </span>

      <button v-if="showReveal" type="button" class="btn btn--secondary" @click="emit('reveal')">
        <AppIcon name="folder-open" :size="15" />
        {{ t('footer.reveal') }}
      </button>

      <button
        v-if="phase === 'preparing'"
        type="button"
        class="btn btn--primary btn--lg"
        disabled
      >
        {{ t('footer.starting') }}
      </button>
      <button
        v-else-if="phase === 'cancelling'"
        type="button"
        class="btn btn--secondary btn--lg"
        disabled
      >
        {{ t('footer.cancelling') }}
      </button>
      <button
        v-else-if="busy"
        type="button"
        class="btn btn--secondary btn--lg"
        @click="emit('cancel')"
      >
        {{ t('common.cancel') }}
      </button>
      <button
        v-else
        type="button"
        class="btn btn--primary btn--lg"
        :disabled="!canStart"
        @click="emit('run')"
      >
        <AppIcon name="play" :size="15" />
        {{ t('footer.run') }}
      </button>
    </div>
  </footer>
</template>

<style scoped>
.foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  min-width: 0;
}

.foot__stats {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  min-width: 0;
  color: var(--ink-2);
  font-size: var(--fs-2);
}

.foot__part {
  color: var(--ink-2);
  white-space: nowrap;
}

.foot__part[data-tone='bad'] {
  color: var(--bad);
  font-weight: 600;
}

.foot__part[data-tone='warn'] {
  color: var(--warn);
}

.foot__sep {
  color: var(--line-3);
}

.foot__actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex: none;
}

.foot__ver {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 3px 6px;
  border: 0;
  border-radius: var(--r-1);
  background: transparent;
  color: var(--ink-3);
  font-size: var(--fs-1);
  cursor: pointer;
  transition:
    color var(--t-fast) var(--ease),
    background var(--t-fast) var(--ease);
}

.foot__ver:hover {
  background: var(--panel-2);
  color: var(--ink);
}

.foot__ver-dot {
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: var(--accent);
}

.foot__kbd {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: var(--ink-3);
  font-size: var(--fs-1);
}

.foot__plus {
  color: var(--ink-3);
}

/* 窄窗口：先丢掉快捷键提示，把空间让给汇总和按钮 */
@media (max-width: 820px) {
  .foot__kbd {
    display: none;
  }
}
</style>
