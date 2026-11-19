<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import type { RunPhase, RunStats } from '../composables/useRun'
import { useI18n, type MessageKey } from '../i18n'

const props = defineProps<{
  stats: RunStats
  phase: RunPhase
  /** 远程图模式：调试构建 + 下载模式下，「没动的远程图」是这一轮没去下，不是按策略保留。 */
  remoteMode?: 'ignore' | 'check' | 'download'
  busy: boolean
  canStart: boolean
  canReveal: boolean
  /** 调试构建：整轮只扫描、不写文件，所以「打开输出目录」必然打空。 */
  debug: boolean
  /**
   * 另一页正在跑时的一句说明（「转换 正在运行」）。
   *
   * 三页可以同时跑，这一页自己的按钮不受别页影响；但站在这一页仍该知道别处
   * 也有活在干——侧栏已经点了记号，底栏再用一句话说清楚。
   */
  busyElsewhere?: string
  /**
   * 主按钮为什么点不了（「正在扫描 2 个文件…」「1 个文件还没扫描通过」）。
   *
   * 扫描是每张卡片自己进行的，等的时候按钮本来就该灰着 —— 但灰着不说话，
   * 站在那儿的人只会以为程序卡了。
   */
  blockedHint?: string
}>()

const emit = defineEmits<{
  run: []
  cancel: []
  reveal: []
}>()

const { t } = useI18n()

/** 底栏那个主按钮：三条作业各说各的动词 —— 只写「开始」没人知道会开始什么。 */
const runKey = computed<MessageKey>(() => {
  if (props.remoteMode === 'check') return 'footer.run.check'
  if (props.remoteMode === 'download') return 'footer.run.download'
  return 'footer.run'
})

type Part = { text: string; tone: 'plain' | 'warn' | 'bad' }

const settled = computed(
  () =>
    props.phase === 'running' ||
    props.phase === 'cancelling' ||
    props.phase === 'done' ||
    props.phase === 'cancelled',
)

/** 英文只有一个的时候不能写「1 files」：按数量挑单复数两套文案。 */
function countText(key: MessageKey, count: number): string {
  const full = (count === 1 ? `${key}.one` : key) as MessageKey
  return t(full, { count })
}

/** 状态栏读数：文件数、引用数，跑完之后追加替换与失败计数。 */
const parts = computed<Part[]>(() => {
  const stats = props.stats
  if (stats.files === 0) return [{ text: t('footer.queueEmpty'), tone: 'plain' }]

  const list: Part[] = [{ text: countText('footer.files', stats.files), tone: 'plain' }]
  if (props.busyElsewhere) list.push({ text: props.busyElsewhere, tone: 'warn' })
  list.push(
    stats.images > 0
      ? { text: countText('footer.images', stats.images), tone: 'plain' }
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
    // 「检测 N 个远程地址 · 其中失效 N」只属于检测页：下载页在「一张都没下来」
    // 的时候 remoteChecked 也是非零，拿这条念会变成「下载没完成 · 检测 2 个地址」。
    if (props.remoteMode === 'check' && stats.remoteChecked > 0 && stats.localized === 0) {
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
          : countText('footer.remoteChecked', stats.remoteChecked)
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
      // 调试构建不会真的去下载：这时「没动的远程图」是没写文件，不是按策略保留。
      const keptKey =
        props.debug && props.remoteMode === 'download'
          ? 'footer.remoteKeptDebug'
          : 'footer.remoteKept'
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

// 调试构建没有真正写出文件，别给一个必然失败的「打开输出目录」
const showReveal = computed(
  () =>
    props.canReveal && !props.debug && (props.phase === 'done' || props.phase === 'cancelled'),
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
    </div>

    <div class="foot__actions">
      <span v-if="!busy && blockedHint" class="foot__hint">{{ blockedHint }}</span>

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
        {{ t(runKey) }}
      </button>
    </div>
  </footer>
</template>

<style scoped>
/* 底栏是窗口的状态条，不是第二张卡片：只有一条上边线，贴着内容区走。
   一条横线 + 一行读数 + 一个主行动 —— 屏幕上只留一处「开始」。 */
.foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  min-width: 0;
  height: var(--foot-h);
  margin: 0;
  padding: 0 18px;
  /* 这是卡片的脚注：左右下三边跟着卡片走，上边一条细线分隔。 */
  border: 1px solid var(--border-panel);
  border-top: 1px solid var(--line);
  border-radius: 0 0 var(--r-4) var(--r-4);
  background: var(--panel);
}

@media (max-width: 900px) {
  .foot {
    height: auto;
    min-height: 44px;
    flex-wrap: wrap;
    padding: 6px 2px;
    row-gap: 6px;
  }
}

.foot__stats {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 7px;
  min-width: 0;
  color: var(--ink-3);
  font-size: var(--fs-1);
}

.foot__part {
  color: var(--ink-2);
  font-variant-numeric: tabular-nums;
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
  gap: 8px;
  flex: none;
}

/* 灰按钮旁边的一句话：说清是谁在扫、还差几个 */
.foot__hint {
  color: var(--ink-3);
  font-size: var(--fs-1);
  white-space: nowrap;
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
