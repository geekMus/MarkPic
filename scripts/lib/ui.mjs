import process from 'node:process';

// 只在真正的终端里上色；重定向到文件或 CI 日志时保持干净。
const colorEnabled =
  process.stdout.isTTY === true && process.env.NO_COLOR === undefined && process.env.TERM !== 'dumb';

function paint(code, text) {
  return colorEnabled ? `\x1b[${code}m${text}\x1b[0m` : text;
}

export const ui = {
  step(message) {
    console.log(paint(36, `==> ${message}`));
  },
  detail(message) {
    console.log(paint(90, `    ${message}`));
  },
  ok(message) {
    console.log(paint(32, message));
  },
  warn(message) {
    console.warn(paint(33, `警告：${message}`));
  },
  error(message) {
    console.error(paint(31, `错误：${message}`));
  },
};
