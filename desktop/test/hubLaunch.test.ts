import { describe, expect, it } from 'vitest'
import * as path from 'node:path'
import { hubLaunch } from '../src/main/hubProcess'

const knobs = {
  SSHGATE_BIN: '/tmp/fakehub', SSHGATE_STORE: '/tmp/v.json', SSHGATE_SSH_CONFIG: '/tmp/ssh_config',
  SSHGATE_IDLE_LOCK: '99h', SSHGATE_RUNTIME_DIR: '/tmp/rt', SSH_AUTH_SOCK: '/tmp/agent', HOME: '/Users/x',
}
const appPath = '/Applications/sshgate.app/Contents/Resources/app'
const bundled = path.join(appPath, '..', 'sshgate')

describe('hubLaunch', () => {
  it('honours the dev knobs in an unpackaged run', () => {
    const l = hubLaunch({ env: knobs, packaged: false, appPath, platform: 'darwin', exists: () => true })
    expect(l.command).toBe('/tmp/fakehub')
    expect(l.args).toEqual(['hub', '--store=/tmp/v.json', '--sshConfig=/tmp/ssh_config', '--idleLock=99h'])
    expect(l.env).toBe(knobs)
  })

  it('ignores every SSHGATE_* knob in the packaged app and keeps the rest of the env', () => {
    const l = hubLaunch({ env: knobs, packaged: true, appPath, platform: 'darwin', exists: (p) => p === bundled })
    expect(l.command).toBe(bundled)
    expect(l.args).toEqual(['hub'])
    expect(Object.keys(l.env).filter((k) => k.startsWith('SSHGATE_'))).toEqual([])
    expect(l.env.SSH_AUTH_SOCK).toBe('/tmp/agent')
    expect(l.env.HOME).toBe('/Users/x')
  })

  it('falls back to PATH when no binary sits next to the app', () => {
    expect(hubLaunch({ env: {}, packaged: true, appPath, platform: 'darwin', exists: () => false }).command).toBe('sshgate')
    expect(hubLaunch({ env: {}, packaged: false, appPath, platform: 'win32', exists: () => false }).command).toBe('sshgate.exe')
  })
})
