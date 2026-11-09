import type { RunRowView } from './useRun'

/** 一处引用：哪篇文档的第几行。 */
export interface DeadRef {
  file: string
  line: number
}

/** 一个打不开的地址，以及它在文档里的全部引用处。 */
export interface DeadLink {
  url: string
  reason: string
  refs: DeadRef[]
  /** 'download' 表示这条是下载失败（连接被拒/超时），清单标题要跟着变。 */
  kind: 'check' | 'download'
}

/** 一张找不到的本地图，以及它在文档里的全部引用处。 */
export interface MissingLocal {
  path: string
  reason: string
  refs: DeadRef[]
}

/** 清单里每个地址最多列几处引用：再多就没人看了。 */
const MAX_REFS = 8

/** 协议相对地址（//host/x.png）也是远程图，只是省了协议。 */
function isRemoteTarget(target: string): boolean {
  const t = target.trim().toLowerCase()
  return t.startsWith('http://') || t.startsWith('https://') || t.startsWith('//')
}

/**
 * 把逐图明细里「打不开」的那几行汇总成按地址去重的清单。
 *
 * 为什么要在界面里做：命令行跑完本来就会打一份去重清单，可界面上只有逐行列着的
 * 红字 —— 一份长文档里同一个死链被引用几十次是常事，要照着改文档只能一行一行抄。
 *
 * 两种状态都算「这个地址没拿到图」：检测模式标 remote-bad（404、返回的不是图片），
 * 下载模式遇到连接被拒/超时会标 failed。命令行那条链路早就是这么合并的，界面这里
 * 以前只认 remote-bad，下载失败的那些地址复制不出来。
 */
export function collectDeadLinks(rows: readonly RunRowView[]): DeadLink[] {
  const byURL = new Map<string, DeadLink>()
  for (const row of rows) {
    const res = row.result
    if (!res?.items) continue
    for (const item of res.items) {
      const bad = item.status === 'remote-bad' || item.status === 'failed'
      if (!bad || !isRemoteTarget(item.target)) continue
      const url = item.target
      let link = byURL.get(url)
      if (!link) {
        link = { url, reason: '', refs: [], kind: item.status === 'failed' ? 'download' : 'check' }
        byURL.set(url, link)
      }
      if (link.reason === '' && item.error) link.reason = item.error
      link.refs.push({ file: row.name, line: item.line })
    }
  }
  // 引用最多的排前面：那是改起来最麻烦、最先要修的
  return [...byURL.values()].sort(
    (a, b) => b.refs.length - a.refs.length || a.url.localeCompare(b.url),
  )
}

/**
 * 把「文档引用了、但文件不在」的本地图汇总起来。
 *
 * 只检测 / 只下载这两种模式不碰本地图，可引用的文件不在照样是坏链接 —— 明细里标
 * 「本地缺图」，行上写「其中 N 张本地图找不到」。这份清单是给人照着改文档用的，
 * 只列远程死链就把本地缺图漏在屏幕上了。
 */
export function collectMissingLocals(rows: readonly RunRowView[]): MissingLocal[] {
  const byPath = new Map<string, MissingLocal>()
  for (const row of rows) {
    const res = row.result
    if (!res?.items) continue
    for (const item of res.items) {
      if (item.status !== 'local-kept' || !item.error) continue
      const path = item.target
      let miss = byPath.get(path)
      if (!miss) {
        miss = { path, reason: '', refs: [] }
        byPath.set(path, miss)
      }
      if (miss.reason === '') miss.reason = item.error
      miss.refs.push({ file: row.name, line: item.line })
    }
  }
  return [...byPath.values()].sort(
    (a, b) => b.refs.length - a.refs.length || a.path.localeCompare(b.path),
  )
}

/**
 * 把地址里的账号密码换成 ***。
 *
 * 这份清单是给人复制走的，多半会被贴进 issue、聊天窗口或者笔记里。地址写成
 * `http://user:pw@host/x.png` 的文档不算罕见（私有图床常见写法），
 * 原样带出去就等于把密码一起公开了。地址本身还在用户自己的文档里，改它不难。
 */
export function redactUrl(target: string): string {
  const s = target.trim()
  // 协议相对地址（//user:pw@host/x.png）没有 `://`，同样是带凭据的地址。
  let start = -1
  const scheme = s.indexOf('://')
  if (scheme >= 0) start = scheme + 3
  else if (s.startsWith('//')) start = 2
  if (start < 0) return s
  const rest = s.slice(start)
  const cut = rest.search(/[/?#]/)
  const authority = cut < 0 ? rest : rest.slice(0, cut)
  const at = authority.lastIndexOf('@')
  if (at < 0) return s
  return `${s.slice(0, start)}***${authority.slice(at)}${rest.slice(authority.length)}`
}

export interface DeadReportStrings {
  title: string
  /** download 模式（有地址是下载失败而不是 404）时用的标题。 */
  titleDownload: string
  summary: (refs: number, urls: number) => string
  refs: (places: string, total: number) => string
  /** 引用之间的分隔符：中文用「、」，英文用「, 」。 */
  sep: string
  more: string
  /** 「找不到的本地图」那一节的标题。 */
  missingTitle: string
  missingSummary: (refs: number, paths: number) => string
}

/**
 * 拼成一段可以直接贴进笔记的 Markdown。
 *
 * 每个地址占三行：地址、原因、引用处。原因自己就带括号（「…（HTTP 404）」），
 * 所以不能把「共 N 处」再接在同一行的括号后面 —— 那样会读成两个并排的括号。
 */
export function formatDeadReport(links: readonly DeadLink[], s: DeadReportStrings): string {
  const totalRefs = links.reduce((n, link) => n + link.refs.length, 0)
  // 标题按数据本身定，不看手上的设置：跑完之后用户可能又去切了模式，
  // 那时候设置已经不是这批结果用的那个了。
  const title = links.some((link) => link.kind === 'download') ? s.titleDownload : s.title
  const out: string[] = [`## ${title}`, '', s.summary(totalRefs, links.length), '']
  for (const link of links) {
    const shown = link.refs.slice(0, MAX_REFS).map((ref) => `${ref.file}:${ref.line}`)
    const places =
      link.refs.length > MAX_REFS ? `${shown.join(s.sep)} ${s.more}` : shown.join(s.sep)
    out.push(`- ${redactUrl(link.url)}`)
    if (link.reason !== '') out.push(`  ${link.reason}`)
    out.push(`  ${s.refs(places, link.refs.length)}`)
  }
  return out.join('\n')
}

/** 「找不到的本地图」那一节。 */
export function formatMissingLocals(
  missing: readonly MissingLocal[],
  s: DeadReportStrings,
): string {
  const totalRefs = missing.reduce((n, miss) => n + miss.refs.length, 0)
  const out: string[] = [`## ${s.missingTitle}`, '', s.missingSummary(totalRefs, missing.length), '']
  for (const miss of missing) {
    const shown = miss.refs.slice(0, MAX_REFS).map((ref) => `${ref.file}:${ref.line}`)
    const places =
      miss.refs.length > MAX_REFS ? `${shown.join(s.sep)} ${s.more}` : shown.join(s.sep)
    out.push(`- ${miss.path}`)
    if (miss.reason !== '') out.push(`  ${miss.reason}`)
    out.push(`  ${s.refs(places, miss.refs.length)}`)
  }
  return out.join('\n')
}

/** 两份清单合成一份：要么没碰到坏链接，要么一次给全。 */
export function formatBrokenReport(
  links: readonly DeadLink[],
  missing: readonly MissingLocal[],
  s: DeadReportStrings,
): string {
  const parts: string[] = []
  if (links.length > 0) parts.push(formatDeadReport(links, s))
  if (missing.length > 0) parts.push(formatMissingLocals(missing, s))
  return parts.join('\n\n')
}

/**
 * 往剪贴板写文本。
 *
 * 先走 navigator.clipboard（127.0.0.1 算安全来源，WebView2 与浏览器都认），
 * 不行再退回老办法：临时塞一个 textarea、选中、execCommand('copy')。
 * 后者虽然过时，但它是唯一在「页面没拿到剪贴板权限」时还能用的路。
 */
export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    /* 落到下面的老办法 */
  }
  const area = document.createElement('textarea')
  area.value = text
  area.setAttribute('readonly', '')
  area.style.position = 'fixed'
  area.style.top = '-1000px'
  area.style.opacity = '0'
  document.body.appendChild(area)
  try {
    area.select()
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    document.body.removeChild(area)
  }
}