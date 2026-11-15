#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { parseArgs } from 'node:util';

import { isEntry } from './lib/cli.mjs';
import { defaultExe, repoRoot } from './lib/paths.mjs';
import { isWindows, startProcess, stopProcess, waitForExit } from './lib/proc.mjs';
import { ui } from './lib/ui.mjs';
import { buildProject } from './build.mjs';

const HELP = `
用法：node scripts/start.mjs [选项]

构建（必要时）并运行 bin/MarkPic.exe。原生窗口目前只有 Windows 版。

选项：
  --build           先重新编译，默认只在 exe 不存在时编译
  --data-dir <路径> 指定数据目录，默认用 exe 旁边的 data/
  --go <路径>       go 可执行文件，默认从 PATH / GOROOT / 常见位置查找
  -h, --help        显示这段帮助
`;

async function main() {
  const { values } = parseArgs({
    allowPositionals: false,
    strict: true,
    options: {
      build: { type: 'boolean', default: false },
      'data-dir': { type: 'string' },
      go: { type: 'string' },
      help: { type: 'boolean', short: 'h', default: false },
    },
  });
  if (values.help) {
    console.log(HELP.trim());
    return;
  }
  if (!isWindows) {
    throw new Error('原生窗口目前只有 Windows 版。非 Windows 上可以用 npm run dev 只起前端，或者用 npm run build:go 编本机二进制。');
  }

  const exe = defaultExe;
  if (values.build || !fs.existsSync(exe)) {
    buildProject({ go: values.go, output: exe });
  } else {
    ui.detail(`使用现有 ${path.relative(repoRoot, exe)}；改过 Go 代码就加 --build。`);
  }

  const env = { ...process.env };
  if (values['data-dir']) {
    const dataDir = path.resolve(process.cwd(), values['data-dir']);
    env.MARKPIC_DATA_DIR = dataDir;
    ui.detail(`数据目录：${dataDir}`);
  }

  ui.step('启动窗口');
  const child = startProcess(exe, [], { cwd: repoRoot, env });
  ui.detail(`pid ${child.pid}（关掉窗口即退出）`);

  let stopping = false;
  const interrupt = () => {
    if (stopping) return;
    stopping = true;
    ui.detail('收到 Ctrl+C，结束窗口');
    void stopProcess(child).finally(() => process.exit(130));
  };
  process.once('SIGINT', interrupt);

  try {
    const result = await waitForExit(child);
    if (result.code !== 0 && result.code !== null) process.exitCode = result.code;
  } finally {
    process.off('SIGINT', interrupt);
  }
}

if (isEntry(import.meta.url)) {
  main().catch((error) => {
    ui.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 1;
  });
}
