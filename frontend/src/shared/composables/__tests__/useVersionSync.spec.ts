import { afterEach, describe, expect, it, vi } from 'vitest'
import { checkVersion, currentBuild, fetchServerBuild } from '../useVersionSync'

function serverBuild(build: unknown, ok = true) {
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok, json: async () => ({ build }) })))
}

describe('useVersionSync', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    document.body.innerHTML = ''
    sessionStorage.clear()
  })

  it('reads the build id without caching', async () => {
    serverBuild('b2')
    expect(await fetchServerBuild()).toBe('b2')
    expect(fetch).toHaveBeenCalledWith('/version.json', expect.objectContaining({ cache: 'no-store' }))
  })

  it('ignores errors and malformed answers', async () => {
    serverBuild('', true)
    expect(await fetchServerBuild()).toBeNull()
    serverBuild('b2', false)
    expect(await fetchServerBuild()).toBeNull()
  })

  it('does nothing while the server runs this build', async () => {
    serverBuild(currentBuild)
    await checkVersion()
    expect(document.getElementById('community-update-toast')).toBeNull()
  })

  it('announces a new server build', async () => {
    serverBuild('next-build')
    await checkVersion()
    expect(document.getElementById('community-update-toast')).not.toBeNull()
  })

  it('does not loop when a reload already targeted that build', async () => {
    sessionStorage.setItem('community:version-reload', 'stale-edge')
    serverBuild('stale-edge')
    await checkVersion()
    expect(document.getElementById('community-update-toast')).toBeNull()
  })
})
