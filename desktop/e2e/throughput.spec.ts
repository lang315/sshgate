import { test, expect } from '@playwright/test'
import * as fs from 'node:fs'
import * as os from 'node:os'
import * as path from 'node:path'
import { launch, openBox, unlock, type Launched } from './launch'

// Checklist item 2 (throughput), automated part: sshtestd floods the terminal
// (200 MiB, as the checklist's `yes | head -c 200M`; 50 MB took only ~1.4 s
// here, too short to see a memory trend) while we watch UI responsiveness
// and memory. A real server over a real network, and the native-terminal
// comparison, stay manual. FLOOD_BYTES overrides the size for exploration.
test.skip(process.platform === 'win32', 'door client uses a Unix socket')

const FLOOD = Number(process.env.FLOOD_BYTES) || 200 * 1024 * 1024
let l: Launched
test.beforeAll(async () => { l = await launch() })
test.afterAll(async () => { await l?.close() })

interface Sample { t: number; main: number; renderer: number; heap: number } // MB: working sets, renderer JS heap

test('200 MiB flood keeps the UI responsive and memory bounded', async () => {
  test.setTimeout(300_000)
  const win = await l.app.firstWindow()
  // An occluded window would otherwise throttle requestAnimationFrame and
  // read as a frozen UI; we want the renderer's own stalls, not the OS's.
  await l.app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].webContents.setBackgroundThrottling(false))
  await unlock(win)
  await openBox(win)
  await win.keyboard.type('echo ready')
  await expect(win.locator('.xterm-rows')).toContainText('echo ready')
  await win.keyboard.press('Enter')

  // Input responsiveness: sshtestd reads input serially, so keys typed during
  // the flood only echo after FLOOD-DONE. Instead we measure (a) how long the
  // renderer takes to handle each keystroke (keyboard.press resolves once
  // the renderer has dispatched the key event) and (b) that every key reached
  // main as a term.write notification (keydown -> xterm onData -> IPC).
  await l.app.evaluate(({ ipcMain }) => {
    const g = globalThis as unknown as { __termWrites: number }
    g.__termWrites = 0
    ipcMain.on('hub:notify', (_e, method) => { if (method === 'term.write') g.__termWrites++ })
  })
  const termWrites = () => l.app.evaluate(() => (globalThis as unknown as { __termWrites: number }).__termWrites)

  // UI responsiveness: the largest gap between animation frames.
  await win.evaluate(() => {
    const w = window as unknown as { __maxGap: number; __frames: number; __stop: boolean }
    w.__maxGap = 0; w.__frames = 0; w.__stop = false
    let last = performance.now()
    const tick = (now: number) => {
      w.__maxGap = Math.max(w.__maxGap, now - last); w.__frames++; last = now
      if (!w.__stop) requestAnimationFrame(tick)
    }
    requestAnimationFrame(tick)
  })

  // performance.memory is bucketed and stale; CDP gives the live JS heap.
  const cdp = await l.app.context().newCDPSession(win)
  await cdp.send('Performance.enable')
  const samples: Sample[] = []
  const sample = async (): Promise<Sample> => {
    const m = await l.app.evaluate(({ app }) => app.getAppMetrics())
    const ws = (type: string) => Math.round(Math.max(0, ...m.filter((p) => p.type === type).map((p) => p.memory.workingSetSize)) / 1024)
    const heap = (await cdp.send('Performance.getMetrics')).metrics.find((x) => x.name === 'JSHeapUsedSize')!.value
    return { t: Date.now(), main: ws('Browser'), renderer: ws('Tab'), heap: Math.round(heap / 1048576) }
  }

  await win.keyboard.type(`flood ${FLOOD}`)
  const writesBefore = await termWrites()
  const start = Date.now()
  await win.keyboard.press('Enter')
  let done = false
  const doneP = expect(win.locator('.xterm-rows')).toContainText('FLOOD-DONE', { timeout: 180_000 }).finally(() => { done = true })
  const memLoop = (async () => { while (!done) { samples.push(await sample()); await new Promise((r) => setTimeout(r, 250)) } })()
  const keyLatency: number[] = []
  const keyLoop = (async () => {
    while (!done) {
      await new Promise((r) => setTimeout(r, 500))
      if (done) break
      const t0 = Date.now()
      await win.keyboard.press('k')
      keyLatency.push(Date.now() - t0)
    }
  })()
  await doneP
  const elapsed = (Date.now() - start) / 1000
  await Promise.all([memLoop, keyLoop])
  const frames = await win.evaluate(() => {
    const w = window as unknown as { __maxGap: number; __frames: number; __stop: boolean }
    w.__stop = true
    return { maxGap: w.__maxGap, frames: w.__frames }
  })
  const writes = (await termWrites()) - writesBefore
  // Retained JS objects, or native memory? Compare before and after a forced GC.
  const beforeGC = await sample()
  await cdp.send('HeapProfiler.collectGarbage')
  await new Promise((r) => setTimeout(r, 1000))
  const afterGC = await sample()

  const third = (i: number) => samples.filter((s) => s.t >= start + (i * elapsed * 1000) / 3 && s.t < start + ((i + 1) * elapsed * 1000) / 3)
  const peak = (ss: Sample[], k: 'main' | 'renderer') => Math.max(0, ...ss.map((s) => s[k]))
  const result = {
    date: new Date().toISOString(),
    machine: `${os.cpus()[0].model}, ${Math.round(os.totalmem() / 2 ** 30)} GB, ${process.platform} ${os.release()} ${process.arch}`,
    floodBytes: FLOOD,
    elapsedS: +elapsed.toFixed(2),
    mbPerS: +(FLOOD / 1e6 / elapsed).toFixed(2),
    maxFrameGapMs: Math.round(frames.maxGap),
    frames: frames.frames,
    keys: { pressed: keyLatency.length + 1, termWrites: writes, maxPressMs: Math.max(0, ...keyLatency) },
    memoryMB: {
      mainPeak: peak(samples, 'main'), rendererPeak: peak(samples, 'renderer'),
      mainFirstThird: peak(third(0), 'main'), mainLastThird: peak(third(2), 'main'),
      rendererFirstThird: peak(third(0), 'renderer'), rendererLastThird: peak(third(2), 'renderer'),
      beforeGC: { main: beforeGC.main, renderer: beforeGC.renderer, rendererHeap: beforeGC.heap },
      afterGC: { main: afterGC.main, renderer: afterGC.renderer, rendererHeap: afterGC.heap },
    },
    // [seconds since Enter, main MB, renderer MB, renderer JS heap MB]
    series: samples.map((s) => [+((s.t - start) / 1000).toFixed(2), s.main, s.renderer, s.heap]),
  }
  console.log('throughput:', JSON.stringify(result, null, 2))
  const out = path.resolve(__dirname, '..', 'test-results')
  fs.mkdirSync(out, { recursive: true })
  fs.writeFileSync(path.join(out, 'throughput.json'), JSON.stringify(result, null, 2) + '\n')

  // Every keystroke (the 'k's plus the Enter) reached main as term.write.
  expect(writes).toBeGreaterThanOrEqual(keyLatency.length + 1)
  expect(result.maxFrameGapMs).toBeLessThanOrEqual(200)
  expect(result.series.length).toBeGreaterThanOrEqual(3)
  expect(peak(third(2), 'main')).toBeLessThanOrEqual(1.5 * peak(third(0), 'main'))
  expect(peak(third(2), 'renderer')).toBeLessThanOrEqual(1.5 * peak(third(0), 'renderer'))
})
