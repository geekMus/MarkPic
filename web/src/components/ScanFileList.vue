<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import { useI18n } from '../i18n'
import type { FileResult } from '../types'

/**
 * 扫描卡片里的文件清单。
 *
 * 扫描卡片回答的是「这个文件夹里有哪些 Markdown」——只给一个份数，用户没法判断
 * 要不要导入。清单只写扫描根目录以下的那一段：整条绝对路径在每行重复一遍没有
 * 信息量，完整路径放 title 里。
 */
const props = defineProps<{
  /** 扫描发现的文件（发现档的 FileResult，只有 path / name / dir）。 */
  files: FileResult[]
  /** 扫描的根（文件夹 / 盘）：清单里只显示它下面那一段。 */
  root?: string
  /** 最多铺多少行；超出的只说个数（上万份时不往 DOM 里塞一万个 li）。 */
  limit?: number
}>()

const { t } = useI18n()

const MAX = computed(() => Math.max(1, props.limit ?? 200))
const root = computed(() => (props.root ?? '').replace(/[\\/]+$/, ''))

function relativePath(file: FileResult): string {
  const base = root.value
  if (base === '') return file.path
  // 拖进来的路径可能是正斜杠（D:/x/y），后端回的是反斜杠（D:\\x\\y）：
  // 比较前统一成一种写法，否则清单会把整条绝对路径原样铺出来。
  const normBase = base.replace(/\\/g, '/').toLowerCase()
  const normPath = file.path.replace(/\\/g, '/').toLowerCase()
  if (normPath === normBase || normPath.startsWith(normBase + '/')) {
    const rest = file.path.slice(base.length).replace(/^[\\/]+/, '')
    if (rest !== '') return rest
  }
  return file.path
}

function nameOf(file: FileResult): string {
  if (file.name) return file.name
  const parts = relativePath(file).split(/[\\/]/)
  return parts[parts.length - 1] ?? file.path
}

/** 文件名前面那一段目录（带结尾分隔符）；根目录下的文件没有这一段。 */
function dirOf(file: FileResult): string {
  const rel = relativePath(file)
  const name = nameOf(file)
  if (rel.length > name.length && rel.endsWith(name)) {
    return rel.slice(0, rel.length - name.length)
  }
  return ''
}

const rows = computed(() =>
  props.files.slice(0, MAX.value).map((file) => ({
    key: file.path,
    path: file.path,
    dir: dirOf(file),
    name: nameOf(file),
  })),
)
const overflow = computed(() => Math.max(0, props.files.length - MAX.value))
</script>

<template>
  <ul v-if="rows.length > 0" class="scan-files">
    <li v-for="row in rows" :key="row.key" class="scan-files__row">
      <AppIcon class="scan-files__icon" name="doc" :size="13" />
      <span class="scan-files__path" :title="row.path">
        <span v-if="row.dir !== ''" class="scan-files__dir">{{ row.dir }}</span>
        <span class="scan-files__name">{{ row.name }}</span>
      </span>
    </li>
    <li v-if="overflow > 0" class="scan-files__more">{{ t('row.scan.more', { count: overflow }) }}</li>
  </ul>
</template>

<style scoped>
.scan-files {
  margin: 0;
  padding: 0;
  max-height: 240px;
  overflow-y: auto;
  overscroll-behavior: contain;
  list-style: none;
}

.scan-files__row {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 30px;
  padding: 0 10px;
  border-bottom: 1px solid var(--line);
  font-size: var(--fs-2);
}

.scan-files__row:last-child {
  border-bottom: 0;
}

.scan-files__icon {
  flex: none;
  color: var(--ink-3);
}

.scan-files__path {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.scan-files__dir {
  color: var(--ink-3);
}

.scan-files__name {
  color: var(--ink);
}

.scan-files__more {
  padding: 8px 10px;
  border-top: 1px solid var(--line);
  color: var(--ink-3);
  font-size: var(--fs-1);
}
</style>
