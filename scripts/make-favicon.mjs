#!/usr/bin/env node
// 把两套品牌标记写成 web/index.html 里的 favicon data URI。
//
// 标签页图标必须离线可用（窗口用 file:// 加载，不额外请求任何文件），所以只能内联
// 成 data URI。两套配色各写一个 <link rel="icon">，暗色那条带
// media="(prefers-color-scheme: dark)"：支持的浏览器会跟着系统明暗换，不支持的
// 就固定用前一条（亮色，和安装图标一致）。
//
// 生成的段落夹在 web/index.html 的 favicon:begin / favicon:end 两行注释之间，
// 每次整段重写 —— 手改那两行之间的内容会被覆盖。
//
// 用法：node scripts/make-favicon.mjs

import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const indexFile = path.join(root, 'web', 'index.html');
const variants = [
  { name: 'light', file: path.join(root, 'assets', 'mark-light.svg'), media: null },
  { name: 'dark', file: path.join(root, 'assets', 'mark-dark.svg'), media: 'dark' },
];
const begin = '<!-- favicon:begin';
const end = '<!-- favicon:end -->';

function toDataUri(file) {
  const text = fs
    .readFileSync(file, 'utf8')
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/>\s+</g, '><')
    .trim()
    .split(/\s+/)
    .join(' ')
    .replace(/"/g, "'")
    .replace(/#/g, '%23');
  return 'data:image/svg+xml,' + text;
}

function main() {
  const html = fs.readFileSync(indexFile, 'utf8');
  const start = html.indexOf(begin);
  const stopAt = html.indexOf(end);
  if (start < 0 || stopAt < start) {
    throw new Error('web/index.html 里找不到 favicon:begin / favicon:end 标记。');
  }
  const stop = stopAt + end.length;
  const eol = html.includes('\r\n') ? '\r\n' : '\n';
  const lines = ['<!-- favicon:begin 由 scripts/make-favicon.mjs 生成，别手改 -->'];
  for (const variant of variants) {
    const media = variant.media ? ' media="(prefers-color-scheme: ' + variant.media + ')"' : '';
    lines.push('    <link rel="icon" type="image/svg+xml"' + media + ' href="' + toDataUri(variant.file) + '" />');
    lines.push('    <!-- 上面这条是 ' + variant.name + ' 配色：' + path.relative(root, variant.file).split(path.sep).join('/') + ' -->');
  }
  lines.push('    ' + end);
  const body = lines.join(eol);
  fs.writeFileSync(indexFile, html.slice(0, start) + body + html.slice(stop), 'utf8');
  console.log('wrote web/index.html (' + (stop - start) + ' -> ' + body.length + ' chars)');
}

main();
