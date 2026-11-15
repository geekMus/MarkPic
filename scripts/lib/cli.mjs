import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

/** 判断某个 .mjs 是不是被 node 直接执行（而不是被别的脚本 import）。 */
export function isEntry(importMetaUrl) {
  const entry = process.argv[1];
  if (!entry) return false;
  const current = fileURLToPath(importMetaUrl);
  const left = path.resolve(entry);
  const right = path.resolve(current);
  return process.platform === 'win32'
    ? left.toLowerCase() === right.toLowerCase()
    : left === right;
}
