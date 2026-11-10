<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import type { RunPhase, RunStats } from '../composables/useRun'
import { useI18n } from '../i18n'

const props = defineProps<{
  stats: RunStats
  phase: RunPhase
  /** 远程图模式：试运行 + 下载模式下，「没动的远程图」是这一轮没去下，不是按策略保留。 */
  remoteMode?: 'ignore' | 'check' | 'download'
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

  // 队列里还留着目录行时，说「1 个文件」会让粘了整个目录的人以为只处理一篇；
  // 目录行开跑之后会展开成一行一个文档，所以这里只在还没展开的时候改口。
  const dirs = stats.dirs
  const filesOnly = stats.files - dirs
  const filePart: Part =
    dirs === 0
      ? { text: t('footer.files', { count: stats.files }), tone: 'plain' }
      : filesOnly === 0
        ? { text: t('footer.dirsOnly', { dirs }), tone: 'plain' }
        : { text: t('footer.filesAndDirs', { files: filesOnly, dirs }), tone: 'plain' }
  const list: Part[] = [filePart]
  list.push(
    stats.images > 0
      ? { text: t('footer.images', { count: stats.images }), tone: 'plain' }
      : { text: t('footer.noImages'), tone: 'plain' },
  )
  // 只碰远程图的两种模式里「替换 0」永远是零，跑起来时也是句废话：
  // 那两个模式一个字节都不上传，版面留给下载/检测那几个数。
  const remoteMode = props.remoteMode ?? 'ignore'
  const remoteOnly =
    remoteMode !== 'ignore' ||
    (settled.value &&
      (stats.localized > 0 ||
        stats.remoteChecked > 0 ||
        stats.filesChecked > 0 ||
        stats.remoteKept > 0))
  if (settled.value) {
    if (stats.uploaded > 0 || !remoteOnly) {
      // 同一份内容被多个名字引用时只上行一次：只报「替换 3 处」会让人以为发了三次。
      const requests = stats.uploadRequests ?? 0
      if (requests > 0 && requests < stats.uploaded) {
        list.push({
          text: t('footer.uploadedDedup', { count: stats.uploaded, requests }),
          tone: 'plain',
        })
      } else {
        list.push({ text: t('footer.uploaded', { count: stats.uploaded }), tone: 'plain' })
      }
    }
    if (stats.reused > 0) {
      list.push({ text: t('footer.reused', { count: stats.reused }), tone: 'plain' })
    }
    // 整轮都命中下载缓存时，一个字节都没下 —— 再说一句「省下 N 张下载」
    // 会和下面的「下载 N 张」自相矛盾。改成一句「沿用上次」。
    const allReused =
      stats.remoteReused > 0 &&
      stats.localizedUnique > 0 &&
      stats.remoteReused >= stats.localizedUnique
    if (stats.remoteReused > 0 && !allReused) {
      list.push({ text: t('footer.remoteReused', { count: stats.remoteReused }), tone: 'plain' })
    }
    // 「失败」有两个口径：几个文件没写成、几张图没成。只写图片数时，
    // 「文档没写成但一张图都没失败」的场合页脚会干脆不提失败（CLI 那边报「失败 1 个」）。
    if (stats.failedFiles > 0 && stats.failed > 0) {
      // 两个数说的是两件事，但并排写两遍「失败」像复读；合成一句就都保住了。
      list.push({
        text: t('footer.failedBoth', { files: stats.failedFiles, images: stats.failed }),
        tone: 'bad',
      })
    } else if (stats.failedFiles > 0) {
      list.push({ text: t('footer.failedFiles', { count: stats.failedFiles }), tone: 'bad' })
    } else if (stats.failed > 0) {
      list.push({ text: t('footer.failedImages', { count: stats.failed }), tone: 'bad' })
    }
    if (stats.localized > 0) {
      // 同一张图被引用多次时 localized 是引用处数，可 images/ 里只有一个文件。
      // 只显示引用处数会让人以为其余的没下下来，所以去重后的张数才是主数。
      const unique = stats.localizedUnique > 0 ? stats.localizedUnique : stats.localized
      if (allReused) {
        list.push({
          text: t('footer.localizedAllReused', { count: unique }),
          tone: 'plain',
        })
      } else if (unique < stats.localized) {
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
    if (stats.remoteKept > 0) {
      // 默认模式不碰远程图，可「没有可处理的图片」那句单看像是一张图都没有。
      const keptKey =
      props.dryRun && props.remoteMode === 'download' ? 'footer.remoteKeptDryRun' : 'footer.remoteKept'
    list.push({ text: t(keptKey, { count: stats.remoteKept }), tone: 'plain' })
    }
    if (stats.remoteUnsupported > 0) {
      // ftp://、data: 这类地址既不算检测过、也不算失效，不单列就没人知道它们存在。
      list.push({
        text: t('footer.remoteUnsupported', { count: stats.remoteUnsupported }),
        tone: 'warn',
      })
    }
  }
  if (props.phase === 'cancelled') {
    // 图床连不上被提前掐断，和用户按了取消不是一件事：前者该让人去看图床。
    const aborted = stats.abortReason !== ''
    list.push({ text: t(aborted ? 'footer.aborted' : 'footer.cancelled'), tone: 'warn' })
  }
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
/* 底栏是浮在画布上的一块 dock：有边界、有阴影、有内边距。
   以前它是平贴在窗口底部的一条，和队列面板之间没有关系，整屏看起来像
   「一张卡片 + 一条横线」。dock 化之后，上下两块是同一种东西。 */
.foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  min-width: 0;
  height: 54px;
  margin: 10px 0 14px;
  padding: 0 14px 0 16px;
  border: 1px solid var(--border-panel);
  border-radius: var(--r-4);
  background: linear-gradient(180deg, color-mix(in srgb, var(--panel) 90%, white 10%), var(--panel));
  box-shadow: var(--shadow-panel);
}

@media (max-width: 900px) {
  .foot {
    height: auto;
    min-height: 54px;
    flex-wrap: wrap;
    padding: 8px 12px;
    row-gap: 6px;
  }
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
