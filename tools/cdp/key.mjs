// 发送一次真实的键盘事件（走 CDP Input 管线，而不是页面里合成 KeyboardEvent —
// 有些处理逻辑会看 isTrusted，合成事件在这些地方永远进不去）。
//
// 用法：node tools/cdp/key.mjs "k" "ctrl" [port]
//   第二参是修饰键，逗号分隔：ctrl、shift、alt、meta；没有就传空串。
//   只支持单字符键 / Enter / Escape / ArrowUp / ArrowDown / Tab / Backspace。
import { connect } from './connect.mjs'

const [, , key = 'Enter', mods = '', port = '9333'] = process.argv

const DOM_KEYS = {
  Enter: { code: 'Enter', windowsVirtualKeyCode: 13, text: '\r' },
  Escape: { code: 'Escape', windowsVirtualKeyCode: 27 },
  ArrowUp: { code: 'ArrowUp', windowsVirtualKeyCode: 38 },
  ArrowDown: { code: 'ArrowDown', windowsVirtualKeyCode: 40 },
  Tab: { code: 'Tab', windowsVirtualKeyCode: 9 },
  Backspace: { code: 'Backspace', windowsVirtualKeyCode: 8 },
}

const info = DOM_KEYS[key] ?? {
  code: /^[a-z]$/i.test(key) ? `Key${key.toUpperCase()}` : key,
  windowsVirtualKeyCode: key.length === 1 ? key.toUpperCase().charCodeAt(0) : 0,
  text: key.length === 1 ? key : undefined,
}

const flags = new Set(mods.split(',').filter(Boolean))
const modifiers = (flags.has('alt') ? 1 : 0) | (flags.has('ctrl') ? 2 : 0) | (flags.has('meta') ? 4 : 0) | (flags.has('shift') ? 8 : 0)

const client = await connect(port)
await client.send('Input.dispatchKeyEvent', {
  type: 'rawKeyDown',
  modifiers,
  key,
  code: info.code,
  windowsVirtualKeyCode: info.windowsVirtualKeyCode,
})
if (info.text) {
  await client.send('Input.dispatchKeyEvent', {
    type: 'char',
    modifiers,
    key,
    code: info.code,
    text: info.text,
    windowsVirtualKeyCode: info.windowsVirtualKeyCode,
  })
}
await client.send('Input.dispatchKeyEvent', {
  type: 'keyUp',
  modifiers,
  key,
  code: info.code,
  windowsVirtualKeyCode: info.windowsVirtualKeyCode,
})
client.close()
console.log(`key ${key}${mods ? ` (${mods})` : ''} sent`)