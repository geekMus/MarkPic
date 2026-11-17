#!/usr/bin/env node
// 生成 GitHub Release 的更新说明：把「上一个 tag 到现在」的提交按类型分组。
//
// 为什么单独一个脚本：发版说明得能本地预览，不能只活在工作流里。
//   node scripts/release-notes.mjs --version 2026.11.17 --dry-run
// 结构照着 magpie 的发布说明来：分类小标题 → 安装 → 完整变更链接。
import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { execFileSync } from 'node:child_process';
import { parseArgs } from 'node:util';

import { isEntry } from './lib/cli.mjs';
import { repoRoot } from './lib/paths.mjs';
import { ui } from './lib/ui.mjs';

const HELP = [
  '用法：node scripts/release-notes.mjs --version <版本> [选项]',
  '',
  '把上一个 tag 到 HEAD 的提交按类型分组，生成 GitHub Release 的更新说明。',
  '',
  '选项：',
  '  --version <号>      本次版本号（不带 v）；默认取当天日期（Asia/Shanghai）',
  '  --from <tag>        从哪个 tag 开始算；默认自动找最近的 tag，找不到就按首个版本算',
  '  --repo <owner/repo> GitHub 仓库，用来拼提交链接和 compare 链接；默认从 package.json 读',
  '  --asset <文件名>    安装包文件名，写进「安装」小节',
  '  --sha256 <摘要>     安装包的 SHA256',
  '  --out <路径>        输出文件，默认 notes.md',
  '  --max <n>           每类最多列多少条，默认 40',
  '  --dry-run           只打印，不写文件',
  '  --github-output     把 oneline / previous / 各类计数写进 $GITHUB_OUTPUT',
  '  -h, --help          显示这段帮助',
].join('\n');

// 分组的先后顺序就是发布说明里的顺序。
// 前缀同时认中文约定（界面：/修复：）和早期用过的英文 conventional commit（feat:/fix:）。
const SECTIONS = [
  { key: 'features', title: '✨ 新功能', prefixes: ['界面', '资源', '新增', '功能', 'feat', 'feature'] },
  { key: 'improves', title: '🔧 改进', prefixes: ['改进', '优化', '重构', '更名', '调整', '体验', '开发体验', '打磨', 'refactor', 'style'] },
  { key: 'fixes', title: '🐛 修复', prefixes: ['修复', 'fix', 'bugfix', 'hotfix'] },
  { key: 'perf', title: '⚡ 性能', prefixes: ['性能', 'perf', 'performance'] },
  { key: 'security', title: '🔒 安全', prefixes: ['安全', 'security'] },
  { key: 'others', title: '📦 其他', prefixes: ['测试', '构建', '依赖', '发布', '文档', '注释', '杂项', 'chore', 'docs', 'test', 'tests', 'build', 'ci', 'deps', 'release'] },
];

// 认不出的前缀（「标题栏：」「托盘：」这种自由前缀）一律算改进，
// 否则发布说明会被「其他」塞满 —— 那等于没分类。
const DEFAULT_SECTION = 'improves';

const PREFIX_TO_SECTION = new Map();
for (const section of SECTIONS) {
  for (const prefix of section.prefixes) PREFIX_TO_SECTION.set(prefix, section.key);
}

/** 发版机器人自己写的提交、合并提交、纯版本号提交，都不进更新说明。 */
export function shouldSkip(subject) {
  const text = String(subject || '').trim();
  if (!text) return true;
  if (/^merge\b/iu.test(text)) return true;
  if (/^release\s+v/iu.test(text)) return true;
  if (/^v?\d{4}\.\d{2}\.\d{2}\b/u.test(text)) return true;
  return false;
}

/** 一条提交主题 → { key, text }：按「前缀：正文」归类，前缀认不出就归到 DEFAULT_SECTION。 */
export function classify(subject) {
  const cleaned = String(subject || '')
    .replace(/\s*\[[^\]]*\]\s*$/u, '')
    .trim();
  const matched = /^([^：:\s]{1,8})[：:]\s*(.*)$/u.exec(cleaned);
  if (!matched) return { key: DEFAULT_SECTION, text: cleaned };
  const key = PREFIX_TO_SECTION.get(matched[1].toLowerCase()) || DEFAULT_SECTION;
  const body = matched[2].trim();
  return { key, text: body || cleaned };
}

/** git log 的「短 SHA \t 主题」文本 → 提交数组。 */
export function parseGitLog(text) {
  const commits = [];
  for (const line of String(text || '').split('\n')) {
    if (!line.trim()) continue;
    const parts = line.split('\t');
    const sha = String(parts[0] || '').trim();
    const subject = parts.slice(1).join('\t').trim();
    if (!subject) continue;
    commits.push({ sha, subject });
  }
  return commits;
}

/** 提交数组 → { features: [...], improves: [...], ... }，每项带好正文和短 SHA。 */
export function groupCommits(commits) {
  const groups = {};
  for (const section of SECTIONS) groups[section.key] = [];
  for (const commit of commits) {
    if (shouldSkip(commit.subject)) continue;
    const result = classify(commit.subject);
    groups[result.key].push({ text: result.text, sha: commit.sha });
  }
  return groups;
}

export function countGroups(groups) {
  const counts = {};
  for (const section of SECTIONS) counts[section.key] = (groups[section.key] || []).length;
  counts.total = SECTIONS.reduce(function (sum, section) { return sum + counts[section.key]; }, 0);
  return counts;
}

/** 一句话摘要：写进 version.json 的 notes，程序里的「有新版本」对话框读的就是它。 */
export function oneLineSummary(counts, isFirstRelease) {
  if (isFirstRelease) return '首个版本。';
  const parts = [];
  if (counts.features) parts.push('新功能 ' + counts.features + ' 项');
  if (counts.improves) parts.push('改进 ' + counts.improves + ' 项');
  if (counts.fixes) parts.push('修复 ' + counts.fixes + ' 项');
  if (counts.perf) parts.push('性能 ' + counts.perf + ' 项');
  if (counts.security) parts.push('安全 ' + counts.security + ' 项');
  if (parts.length === 0) return '维护性更新。';
  return '本次更新：' + parts.join('，') + '。';
}

function commitLink(repo, sha) {
  if (!repo || !sha) return '';
  return '（[' + sha + '](https://github.com/' + repo + '/commit/' + sha + ')）';
}

/** 渲染成 GitHub Release 正文。 */
export function renderNotes(options) {
  const groups = options.groups;
  const counts = countGroups(groups);
  const lines = [];

  lines.push('## v' + options.version + '（' + options.date + '）');
  lines.push('');
  lines.push(options.oneline);

  for (const section of SECTIONS) {
    const items = groups[section.key] || [];
    if (items.length === 0) continue;
    lines.push('');
    lines.push('### ' + section.title);
    const shown = items.slice(0, options.max);
    for (const item of shown) {
      lines.push('- ' + item.text + commitLink(options.repo, item.sha));
    }
    if (items.length > shown.length) {
      lines.push('- 另有 ' + (items.length - shown.length) + ' 条见下方完整变更');
    }
  }

  lines.push('');
  lines.push('### 安装');
  if (options.asset) {
    lines.push('- 下载 ' + options.asset);
  } else {
    lines.push('- 下载 Releases 页里的 Windows 安装包');
  }
  if (options.sha256) lines.push('- SHA256 ' + options.sha256);
  lines.push('- 老版本会自己提示更新；也可以直接覆盖旧的 exe。');

  lines.push('');
  if (options.previous && options.repo) {
    const range = options.previous + '...v' + options.version;
    lines.push('**完整变更**：[' + range + '](https://github.com/' + options.repo + '/compare/' + range + ')');
  } else if (options.previous) {
    lines.push('**完整变更**：' + options.previous + '...v' + options.version);
  } else {
    lines.push('**完整变更**：这是首个版本。');
  }

  lines.push('');
  return { text: lines.join('\n'), counts: counts };
}

function readPackageJson() {
  try {
    return JSON.parse(fs.readFileSync(path.join(repoRoot, 'package.json'), 'utf8'));
  } catch {
    return {};
  }
}

/** 从 package.json 的 repository.url 里挖出 owner/repo。 */
function repoFromPackageJson(pkg) {
  const url = pkg && pkg.repository && pkg.repository.url ? String(pkg.repository.url) : '';
  const matched = /github\.com[:/]+([^/]+)\/([^/.]+?)(?:\.git)?$/u.exec(url);
  if (!matched) return '';
  return matched[1] + '/' + matched[2];
}

function git(args) {
  return execFileSync('git', args, { cwd: repoRoot, encoding: 'utf8' });
}

function previousTag() {
  try {
    return git(['describe', '--tags', '--abbrev=0', 'HEAD']).trim();
  } catch {
    return '';
  }
}

function readCommits(from) {
  const range = from ? from + '..HEAD' : 'HEAD';
  const text = git(['log', '--no-merges', '--format=%h%x09%s', range]);
  return parseGitLog(text);
}

function todayInShanghai() {
  const formatter = new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Asia/Shanghai',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  });
  return formatter.format(new Date());
}

function main() {
  const parsed = parseArgs({
    allowPositionals: false,
    strict: true,
    options: {
      version: { type: 'string' },
      from: { type: 'string' },
      repo: { type: 'string' },
      asset: { type: 'string' },
      sha256: { type: 'string' },
      out: { type: 'string' },
      date: { type: 'string' },
      max: { type: 'string' },
      'dry-run': { type: 'boolean', default: false },
      'github-output': { type: 'boolean', default: false },
      help: { type: 'boolean', short: 'h', default: false },
    },
  });
  const values = parsed.values;

  if (values.help) {
    console.log(HELP);
    return;
  }

  const pkg = readPackageJson();
  // 版本号不写进任何 package.json：发版号由 tag 定，平时预览按当天日期算，
  // 和流水线的 CalVer 规则一致（当天第一次发就是当天日期）。
  const version = values.version || todayInShanghai().replace(/-/gu, '.');

  const repo = values.repo || (process.env.GITHUB_REPOSITORY || repoFromPackageJson(pkg));
  const from = values.from !== undefined ? values.from : previousTag();
  const commits = readCommits(from);
  const groups = groupCommits(commits);
  const counts = countGroups(groups);
  const isFirstRelease = !from;
  const oneline = oneLineSummary(counts, isFirstRelease);

  const rendered = renderNotes({
    version: version,
    date: values.date || todayInShanghai(),
    groups: groups,
    oneline: oneline,
    repo: repo,
    previous: from,
    asset: values.asset || '',
    sha256: values.sha256 || '',
    max: Math.max(1, Number(values.max || 40)),
  });

  if (values['dry-run']) {
    console.log(rendered.text);
  } else {
    const out = values.out ? path.resolve(process.cwd(), values.out) : path.join(repoRoot, 'notes.md');
    fs.writeFileSync(out, rendered.text, 'utf8');
    ui.ok('已生成 ' + path.relative(repoRoot, out));
  }

  ui.detail('范围 ' + (from || '（首个版本，全部历史）') + ' → HEAD');
  ui.detail('共 ' + commits.length + ' 条提交，收录 ' + counts.total + ' 条');

  if (values['github-output'] && process.env.GITHUB_OUTPUT) {
    const lines = [
      'oneline=' + oneline,
      'previous=' + (from || ''),
      'version=' + version,
      'total=' + counts.total,
    ];
    for (const section of SECTIONS) lines.push(section.key + '=' + counts[section.key]);
    fs.appendFileSync(process.env.GITHUB_OUTPUT, lines.join('\n') + '\n', 'utf8');
  }
}

if (isEntry(import.meta.url)) {
  try {
    main();
  } catch (error) {
    ui.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 1;
  }
}
