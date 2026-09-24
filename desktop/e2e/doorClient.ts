import * as net from 'node:net'

export function doorCall(socketPath: string, method: string, params: unknown, timeoutMs = 30000): Promise<any> {
  return new Promise((resolve, reject) => {
    const sock = net.connect(socketPath)
    let buf = ''
    const t = setTimeout(() => { sock.destroy(); reject(new Error('door call timed out')) }, timeoutMs)
    sock.on('connect', () => sock.write(JSON.stringify({ jsonrpc: '2.0', id: 1, method, params }) + '\n'))
    sock.on('data', (d) => {
      buf += d.toString()
      const nl = buf.indexOf('\n')
      if (nl < 0) return
      clearTimeout(t)
      sock.end()
      const m = JSON.parse(buf.slice(0, nl))
      if (m.error) reject(new Error(m.error.message))
      else resolve(m.result)
    })
    sock.on('error', (e) => { clearTimeout(t); reject(e) })
  })
}
