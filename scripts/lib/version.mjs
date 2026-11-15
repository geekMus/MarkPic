import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';

import { repoRoot } from './paths.mjs';
import { git, gitOrNull } from './proc.mjs';

const RELEASE_RE = /^(\d{4})\.(\d{2})\.(\d{2})(?:\.(\d+))?$/u;
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

export function tagExists(version) {
  return gitOrNull(['rev-parse', '-q', '--verify', `refs/tags/v${version}`]) !== null;
}

/** 发布号：显式 --version > 今天，且自动跳过已经存在的 tag。 */
export function resolveReleaseVersion(explicit = '') {
  if (explicit) {
    const value = String(explicit).trim();
    if (!RELEASE_RE.test(value) || !isRealDate(value)) {
      throw new Error(`发布号「${explicit}」不是日期形式（例如 2026.11.14 或 2026.11.14.1）。`);
    }
    if (tagExists(value)) {
      throw new Error(`tag v${value} 已经存在，换一个发布号。`);
    }
    return value;
  }

  const base = today();
  for (let index = 0; index < 100; index += 1) {
    const candidate = index === 0 ? base : `${base}.${index}`;
    if (!tagExists(candidate)) return candidate;
  }
  throw new Error(`今天（${base}）的发布号已经用掉 100 个了，先检查一下 tag。`);
}

export function resolveCommit() {
  const text = gitOrNull(['rev-parse', '--short', 'HEAD']);
  return text ? text.trim() : 'none';
}

export function sha256(file) {
  return crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex');
}

/**
 * 写仓库根的 version.json。保持固定字段顺序、LF、无 BOM，
 * 这样 CI 与本地生成的文件可以直接比较。
 */
export function writeManifest(options) {
  const { version, notes = '', file } = options;
  const asset = path.basename(file);
  const url = `https://github.com/geekMus/MarkPic/releases/download/v${version}/${asset}`;
  const manifest = {
    version,
    tag: `v${version}`,
    published: options.published ?? today(),
    notes,
    windows: {
      amd64: {
        url,
        sha256: options.sha256 ?? sha256(file),
        size: options.size ?? fs.statSync(file).size,
      },
    },
  };
  fs.writeFileSync(path.join(repoRoot, 'version.json'), JSON.stringify(manifest, null, 2) + '\n', 'utf8');
  return manifest;
}
