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

export interface DeadReportStrings {
  title: string
  /** download 模式（有地址是下载失败而不是 404）时用的标题。 */
  titleDownload: string
  summary: (refs: number, urls: number) => string
  refs: (places: string, total: number) => string
  /** 引用之间的分隔符：中文用「、」，英文用「, 」。 */
  sep: string
  more: string
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
    out.push(`- ${link.url}`)
    if (link.reason !== '') out.push(`  ${link.reason}`)
    out.push(`  ${s.refs(places, link.refs.length)}`)
  }
  return out.join('\n')
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
