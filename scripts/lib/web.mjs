import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { createRequire } from 'node:module';

import { repoRoot, webDir } from './paths.mjs';
import { isWindows, run, startProcess, which } from './proc.mjs';
import { ui } from './ui.mjs';

const requireFromWeb = createRequire(path.join(webDir, 'package.json'));
const binCache = new Map();

function packageJsonPath(pkg) {
  return requireFromWeb.resolve(`${pkg}/package.json`);
}

/** 从 web/node_modules 里找 vite / vue-tsc 的真实入口，避开 pnpm 的 .cmd 包装。 */
export function resolveWebBin(pkg) {
  if (binCache.has(pkg)) return binCache.get(pkg);
  const metaPath = packageJsonPath(pkg);
  const meta = JSON.parse(fs.readFileSync(metaPath, 'utf8'));
  const declared =
    typeof meta.bin === 'string'
      ? meta.bin
      : (meta.bin?.[pkg] ?? (meta.bin ? Object.values(meta.bin)[0] : ''));
  if (!declared) throw new Error(`${pkg} 的 package.json 里没有 bin 字段。`);
  const bin = path.resolve(path.dirname(metaPath), declared);
  binCache.set(pkg, bin);
  return bin;
}

export function webDependenciesReady() {
  try {
    resolveWebBin('vite');
    resolveWebBin('vue-tsc');
    return true;
  } catch {
    return false;
  }
}

function installWebDependencies() {
  const pnpm = which('pnpm') ?? which('pnpm.cmd');
  if (pnpm) {
    run(pnpm, ['--dir', 'web', 'install'], {
      cwd: repoRoot,
      shell: isWindows && /\.(cmd|bat)$/iu.test(pnpm),
      label: '安装前端依赖',
    });
    return;
  }
  const corepack = which('corepack');
  if (corepack) {
    run(corepack, ['pnpm', '--dir', 'web', 'install'], {
      cwd: repoRoot,
      shell: isWindows && /\.(cmd|bat)$/iu.test(corepack),
      label: '安装前端依赖',
    });
    return;
  }
  throw new Error(
    'web/node_modules 不存在，而且找不到 pnpm。先装 Node 20+ 和 pnpm（npm i -g pnpm），或者用 corepack enable 打开内置 pnpm，再重新执行。',
  );
}

export function ensureWebDependencies() {
  if (webDependenciesReady()) return;
  ui.step('安装前端依赖（第一次需要，之后会跳过）');
  installWebDependencies();
  if (!webDependenciesReady()) {
    throw new Error('前端依赖装完了，但还是找不到 vite / vue-tsc。删掉 web/node_modules 再试一次。');
  }
}

export function runWebBin(pkg, args, options = {}) {
  const bin = resolveWebBin(pkg);
  run(process.execPath, [bin, ...args], {
    cwd: webDir,
    label: options.label ?? pkg,
    echo: options.echo,
  });
}

export function typecheckWeb() {
  runWebBin('vue-tsc', ['--noEmit'], { label: '前端类型检查' });
}

export function embeddedUiReady() {
  return fs.existsSync(path.join(repoRoot, 'internal', 'ui', 'dist', 'index.html'));
}

/** 前端产物是 go:embed 的输入，不在版本库里：没有就先建一次。 */
export function ensureEmbeddedUi() {
  if (embeddedUiReady()) return false;
  ui.detail('internal/ui/dist 还没有前端产物，先建一次。');
  buildWeb();
  return true;
}

/** 明确要求不建前端时用：产物缺了就报错，不要静默编出一个没有界面的 exe。 */
export function requireEmbeddedUi() {
  if (embeddedUiReady()) return;
  throw new Error('internal/ui/dist 里没有前端产物：先跑 npm run build（或 node scripts/build.mjs），再试 --no-web。');
}

export function buildWeb() {
  ensureWebDependencies();
  ui.step('构建前端（Vue 3 + Vite → internal/ui/dist）');
  typecheckWeb();
  runWebBin('vite', ['build'], { label: 'Vite 构建' });
  ui.ok('前端构建完成。');
}

/** 起 Vite；返回子进程，调用方负责 stopProcess。 */
export function startVite(port) {
  const bin = resolveWebBin('vite');
  return startProcess(
    process.execPath,
    [bin, '--host', '127.0.0.1', '--port', String(port), '--strictPort'],
    { cwd: webDir, stdio: 'inherit' },
  );
}
