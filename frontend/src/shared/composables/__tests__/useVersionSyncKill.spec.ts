import { afterEach, describe, expect, it, vi } from 'vitest'
import { fetchServerBuild, handleChunkLoadError } from '../useVersionSync'
import { registerUnsavedWork } from '../useUnsavedWork'

describe('useVersionSync kill switch and chunk errors', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    sessionStorage.clear()
    localStorage.clear()
  })

  it('unregisters workers and clears community caches when the server disables the worker', async () => {
    const unregister = vi.fn(async () => true)
    Object.defineProperty(navigator, 'serviceWorker', {
      configurable: true,
      value: { getRegistrations: async () => [{ unregister }], addEventListener: () => {} },
    })
    const del = vi.fn(async () => true)
    vi.stubGlobal('caches', { keys: async () => ['community-assets-b1', 'other'], delete: del })
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: true,
      headers: new Headers({ 'X-Community-Service-Worker': 'off' }),
      json: async () => ({ build: 'b1' }),
    })))
    expect(await fetchServerBuild()).toBe('b1')
    await vi.waitFor(() => expect(del).toHaveBeenCalledWith('community-assets-b1'))
    expect(unregister).toHaveBeenCalled()
    expect(del).not.toHaveBeenCalledWith('other')
  })

  it('persists drafts before reloading on a chunk load failure, once a minute', () => {
    const reload = vi.fn()
    vi.stubGlobal('location', { ...window.location, reload })
    const off = registerUnsavedWork('compose', { busy: () => true, snapshot: () => ({ text: 'hello' }) })
    const event = new Event('vite:preloadError', { cancelable: true })
    handleChunkLoadError(event)
    expect(reload).toHaveBeenCalledTimes(1)
    expect(event.defaultPrevented).toBe(true)
    expect(JSON.stringify(Object.values({ ...localStorage }))).toContain('hello')
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: false })))
    handleChunkLoadError(new Event('vite:preloadError', { cancelable: true }))
    expect(reload).toHaveBeenCalledTimes(1)
    off()
  })
})
