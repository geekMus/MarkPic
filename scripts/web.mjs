#!/usr/bin/env node
import process from 'node:process';
import { parseArgs } from 'node:util';

import { isEntry } from './lib/cli.mjs';
import { ui } from './lib/ui.mjs';
import { buildWeb, ensureWebDependencies, typecheckWeb } from './lib/web.mjs';

const HELP = `
用法：node scripts/web.mjs [选项]

构建前端（vue-tsc --noEmit + vite build）。

选项：
  --check    只做类型检查，不产出 dist
  -h, --help 显示这段帮助
`;

function main() {
  const { values } = parseArgs({
    allowPositionals: false,
    strict: true,
    options: {
      check: { type: 'boolean', default: false },
      help: { type: 'boolean', short: 'h', default: false },
    },
  });
  if (values.help) {
    console.log(HELP.trim());
    return;
  }
  ensureWebDependencies();
  if (values.check) {
    ui.step('前端类型检查');
    typecheckWeb();
    ui.ok('类型检查通过。');
    return;
  }
  buildWeb();
}

if (isEntry(import.meta.url)) {
  try {
    main();
  } catch (error) {
    ui.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 1;
  }
}
