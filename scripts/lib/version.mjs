import crypto from 'node:crypto';
import fs from 'node:fs';
import process from 'node:process';

import { git, gitOrNull } from './proc.mjs';

const BUILD_RE = /^(\d{4})\.(\d{2})\.(\d{2})(?:\.(\d+))?(?:\+dev)?$/u;

function pad(value) {
  return String(value).padStart(2, '0');
}

/** 本地日期，和「今天」在用户机器上的感觉一致。 */
export function today() {
  const now = new Date();
  return `${now.getFullYear()}.${pad(now.getMonth() + 1)}.${pad(now.getDate())}`;
}

export function isRealDate(value) {
  const match = BUILD_RE.exec(String(value));
  if (!match) return false;
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const date = new Date(Date.UTC(year, month - 1, day));
  return (
    date.getUTCFullYear() === year &&
    date.getUTCMonth() === month - 1 &&
    date.getUTCDate() === day
  );
}

function describeTag() {
  const text = gitOrNull(['describe', '--tags', '--abbrev=0']);
  if (!text) return '';
  return text.trim();
}

/** 构建号：显式 --version > 最近的日期 tag > 今天+dev。 */
export function resolveBuildVersion(explicit = '') {
  if (explicit) {
    const value = String(explicit).trim();
    if (!BUILD_RE.test(value) || !isRealDate(value)) {
      throw new Error(`版本号「${explicit}」不是日期形式（例如 2026.11.14 或 2026.11.14+dev）。`);
    }
    return value;
  }

  const described = describeTag();
  const match = /^v?(\d{4}\.\d{2}\.\d{2}(?:\.\d+)?)$/u.exec(described);
  if (match && isRealDate(match[1])) return match[1];
  return `${today()}+dev`;
}

export function resolveCommit() {
  const text = gitOrNull(['rev-parse', '--short', 'HEAD']);
  return text ? text.trim() : 'none';
}

export function sha256(file) {
  return crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex');
}

