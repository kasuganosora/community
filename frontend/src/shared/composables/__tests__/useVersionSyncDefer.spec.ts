import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

async function load() {
  vi.resetModules()
  const unsaved = await import('../useUnsavedWork')
  const sync = await import('../useVersionSync')
  return { ...unsaved, ...sync }
}
const reload = vi.fn()
let hidden = false

describe('deploy reload deferral', () => {
  beforeEach(() => {
    vi.useFakeTimers({ now: Date.now() })
    reload.mockReset(); hidden = false
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, json: async () => ({ build: 'next-build' }) })))
    Object.defineProperty(window, 'location', { configurable: true, value: { ...window.location, reload } })
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => hidden ? 'hidden' : 'visible' })
  })
  afterEach(() => {
    vi.useRealTimers(); vi.unstubAllGlobals()
    localStorage.clear(); sessionStorage.clear(); document.body.innerHTML = ''
  })

  it('persists drafts and waits while a composer has unsent text, then reloads once empty and idle', async () => {
    const m = await load()
    let text = 'half-written post'
    m.registerUnsavedWork('post:new:', { busy: () => !!text, snapshot: () => text ? { text } : null })
    vi.advanceTimersByTime(20_000)
    await m.checkVersion()
    expect(reload).not.toHaveBeenCalled()
    expect(JSON.parse(localStorage.getItem('community:draft:post:new:')!).value).toEqual({ text: 'half-written post' })
    text = ''
    await m.checkVersion() // same build: no second announcement, deferral stays pending
    expect(reload).not.toHaveBeenCalled()
    hidden = true
    document.dispatchEvent(new Event('visibilitychange'))
    // startVersionSync is not running here; hidden tabs reload on the next check.
    const again = await load()
    again.registerUnsavedWork('post:new:', { busy: () => true, snapshot: () => ({ text: 'x' }) })
    await again.checkVersion()
    expect(reload).toHaveBeenCalledTimes(1)
    expect(sessionStorage.getItem('community:version-reload')).toBe('next-build')
  })

  it('reloads a hidden tab immediately, keeping the draft', async () => {
    const m = await load()
    hidden = true
    m.registerUnsavedWork('dm:1', { busy: () => true, snapshot: () => ({ text: 'hi' }) })
    await m.checkVersion()
    expect(reload).toHaveBeenCalledTimes(1)
    expect(m.takePersistedDraft('dm:1')).toEqual({ text: 'hi' })
    expect(m.takePersistedDraft('dm:1')).toBeNull()
  })

  it('reloads only once per new build id', async () => {
    const m = await load()
    vi.advanceTimersByTime(20_000)
    await m.checkVersion()
    expect(reload).toHaveBeenCalledTimes(1)
    const next = await load()
    await next.checkVersion()
    expect(reload).toHaveBeenCalledTimes(1)
  })

  it('treats an open modal as work in progress', async () => {
    const m = await load()
    document.body.innerHTML = '<dialog open>busy</dialog>'
    expect(m.hasUnsavedWork()).toBe(true)
  })
})
