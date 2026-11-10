// 连到窗口里的 WebView2（CDP），给 eval.mjs / shot.mjs 用。
//
// 只用 Node 自带的 fetch 与 WebSocket，不装依赖。窗口要先开调试端口：
//   $env:WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS = '--remote-debugging-port=9333'
export async function connect(port = '9333') {
  const targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()
  const page = targets.find(target => target.type === 'page')
  if (!page) throw new Error('窗口里没有页面：先确认程序在跑，且调试端口开着')

  const socket = new WebSocket(page.webSocketDebuggerUrl)
  let seq = 0
  const waiting = new Map()
  socket.addEventListener('message', event => {
    const message = JSON.parse(event.data)
    const slot = message.id && waiting.get(message.id)
    if (!slot) return
    waiting.delete(message.id)
    message.error ? slot.reject(new Error(JSON.stringify(message.error))) : slot.resolve(message.result)
  })
  await new Promise((resolve, reject) => {
    socket.addEventListener('open', resolve, { once: true })
    socket.addEventListener('error', reject, { once: true })
  })

  const send = (method, params = {}) => {
    const id = ++seq
    return new Promise((resolve, reject) => {
      waiting.set(id, { resolve, reject })
      socket.send(JSON.stringify({ id, method, params }))
    })
  }
  return { send, close: () => socket.close() }
}

// evaluate 把一段表达式丢进页面执行，返回它的值（对象会转成 JSON 文本）。
export async function evaluate(port, expression, { awaitPromise = false } = {}) {
  const { send, close } = await connect(port)
  try {
    const reply = await send('Runtime.evaluate', {
      expression,
      returnByValue: true,
      awaitPromise,
    })
    if (reply.exceptionDetails) {
      const detail = reply.exceptionDetails.exception?.description ?? JSON.stringify(reply.exceptionDetails)
      throw new Error(detail)
    }
    const value = reply.result?.value
    return typeof value === 'string' ? value : JSON.stringify(value ?? null)
  } finally {
    close()
  }
}