import { afterEach, describe, expect, it, vi } from 'vitest'
import { currentBuild, fetchServerBuild, handleChunkLoadError } from '../useVersionSync'
import { registerUnsavedWork } from '../useUnsavedWork'

describe('useVersionSync kill switch and chunk errors', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    sessionStorage.clear()
    localStorage.clear()
    document.body.innerHTML = ''
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

  it('reloads a failed chunk only after confirming a new server build, once a minute', async () => {
    const reload = vi.fn()
    vi.stubGlobal('location', { ...window.location, reload })
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, json: async () => ({ build: 'confirmed-new-build' }) })))
    await handleChunkLoadError()
    expect(reload).toHaveBeenCalledTimes(1)
    await handleChunkLoadError()
    expect(reload).toHaveBeenCalledTimes(1)
  })

  it('keeps drafts and attachments in memory during a confirmed deploy', async () => {
    const reload = vi.fn()
    vi.stubGlobal('location', { ...window.location, reload })
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, json: async () => ({ build: 'deploy-with-draft' }) })))
    const off = registerUnsavedWork('compose', { busy: () => true, snapshot: () => ({ text: 'hello' }) })
    await handleChunkLoadError()
    expect(reload).not.toHaveBeenCalled()
    expect(document.getElementById('community-update-toast')).not.toBeNull()
    expect(JSON.stringify(Object.values({ ...localStorage }))).toContain('hello')
    off()
  })

  it.each([currentBuild, null])('does not reload on a transient chunk failure (server build %s)', async build => {
    const reload = vi.fn()
    vi.stubGlobal('location', { ...window.location, reload })
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: !!build, json: async () => ({ build }) })))
    await handleChunkLoadError()
    expect(reload).not.toHaveBeenCalled()
    expect(sessionStorage.getItem('community:preload-reload')).toBeNull()
  })
})
