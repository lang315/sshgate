// Fake ssh-mcp hub for tests. FAKE_HUB_MODE: "ok" | "crash" | "badproto".
import readline from 'node:readline'

const mode = process.env.FAKE_HUB_MODE ?? 'ok'
if (mode === 'crash') {
  process.stderr.write('boom: fake hub crashed\n')
  process.exit(3)
}
const rl = readline.createInterface({ input: process.stdin })
const send = (m) => process.stdout.write(JSON.stringify({ jsonrpc: '2.0', ...m }) + '\n')
rl.on('line', (line) => {
  const m = JSON.parse(line)
  if (m.id === undefined) {
    if (m.method === 'term.write') send({ method: 'term.data', params: { id: m.params.id, data: m.params.data } })
    return
  }
  switch (m.method) {
    case 'hello':
      return send({ id: m.id, result: { protocol: mode === 'badproto' ? 99 : 1 } })
    case 'status':
      return send({ id: m.id, result: { locked: true, hasStore: true, pending: 0 } })
    case 'fail':
      return send({ id: m.id, error: { code: -32000, message: 'nope' } })
    case 'exit':
      send({ id: m.id, result: {} })
      return process.exit(1)
    default:
      return send({ id: m.id, error: { code: -32601, message: 'method not found: ' + m.method } })
  }
})
rl.on('close', () => process.exit(0))
