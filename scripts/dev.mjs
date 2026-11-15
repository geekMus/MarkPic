#!/usr/bin/env node
import process from 'node:process';
import { parseArgs } from 'node:util';

import { isEntry } from './lib/cli.mjs';
import { defaultExe, repoRoot } from './lib/paths.mjs';
import { isWindows, startProcess, stopProcess, waitForExit, waitForPort } from './lib/proc.mjs';
import { ui } from './lib/ui.mjs';
import { buildProject } from './build.mjs';
import { ensureWebDependencies, startVite } from './lib/web.mjs';

const HELP = `
用法：node scripts/dev.mjs [选项]

开发模式：Vite 热更新 + 原生窗口（Windows）。非 Windows 只起 Vite，方便改界面。

选项：
  --port <端口>  Vite 端口，默认 5173
  --go <路径>    go 可执行文件，默认从 PATH / GOROOT / 常见位置查找
  -h, --help     显示这段帮助
`;

async function waitForVite(child, port) {
  const exited = new Promise((resolve) => {
    child.once('exit', (code, signal) => resolve({ exited: true, code, signal }));
  });
  const ready = waitForPort(port, 40000).then((ok) => ({ ready: ok }));
  const first = await Promise.race([exited, ready]);
  if (first.exited) {
    throw new Error(`Vite 提前退出（退出码 ${first.code ?? first.signal ?? '未知'}）。`);
  }
  if (!first.ready) {
    throw new Error(`Vite 没有在 40 秒内起来（端口 ${port}）。`);
  }
}

async function main() {
  const { values } = parseArgs({
    allowPositionals: false,
    strict: true,
    options: {
      port: { type: 'string', default: '5173' },
      go: { type: 'string' },
      help: { type: 'boolean', short: 'h', default: false },
    },
  });
  if (values.help) {
    console.log(HELP.trim());
    return;
  }
  const port = Number(values.port);
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    throw new Error(`端口「${values.port}」不对，应该在 1 到 65535 之间。`);
  }

  ensureWebDependencies();
  // 非 Windows 没有原生窗口，只起 Vite；别为了调界面先要求装 Go。
  if (isWindows) buildProject({ go: values.go, debug: true, output: defaultExe });

  ui.step(`启动 Vite（127.0.0.1:${port}）`);
  const vite = startVite(port);
  let app = null;
  let cleaning = false;

  const cleanup = async () => {
    if (cleaning) return;
    cleaning = true;
    if (app) await stopProcess(app);
    await stopProcess(vite);
  };
  const interrupt = () => {
    ui.detail('收到 Ctrl+C，收掉窗口和 Vite');
    void cleanup().finally(() => process.exit(130));
  };
  process.once('SIGINT', interrupt);

  try {
    await waitForVite(vite, port);
    if (!isWindows) {
      ui.warn('原生窗口只有 Windows 版。Vite 已经起来，可以在浏览器打开下面的地址调界面；bridge / 文件对话框等功能不可用。');
      console.log(`    http://127.0.0.1:${port}`);
      console.log('    按 Ctrl+C 结束。');
      // Ctrl+C 由上面的 interrupt 收尾；这里只等 Vite 自己退出。
      await waitForExit(vite);
      return;
    }

    ui.step('启动窗口（界面来自 Vite，Go 侧全是真的；这一轮是调试模式）');
    app = startProcess(defaultExe, [], {
      cwd: repoRoot,
      env: { ...process.env, MARKPIC_DEV_URL: `http://127.0.0.1:${port}` },
    });
    ui.detail('改 .vue 会热更新；关掉窗口即退出，脚本会把 Vite 一起收掉。');
    const result = await waitForExit(app);
    if (result.code !== 0 && result.code !== null) process.exitCode = result.code;
  } finally {
    process.off('SIGINT', interrupt);
    await cleanup();
  }
}

if (isEntry(import.meta.url)) {
  main().catch((error) => {
    ui.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 1;
  });
}
