import { beforeEach, describe, expect, it, vi } from 'vitest'

const get = vi.fn()
vi.mock('@/shared/api/client', () => ({
  default: { get: (...args: unknown[]) => get(...args) },
  tokenService: { get: () => null, set: () => {}, clear: () => {} },
}))

describe('document title', () => {
  beforeEach(() => {
    vi.resetModules()
    get.mockReset()
    document.title = 'Community'
    document.head.innerHTML = ''
  })

  it('uses the shell application-name before the instance document loads', async () => {
    document.head.innerHTML = '<meta name="application-name" content="メイド🎀">'
    const { pageTitle } = await import('../routes')
    expect(pageTitle('home')).toBe('首页 - メイド🎀')
    expect(pageTitle('lists')).toBe('列表 - メイド🎀')
    expect(pageTitle(undefined)).toBe('メイド🎀')
  })

  it('uses the instance title on every route and refreshes the open tab', async () => {
    get.mockResolvedValue({ data: { title: '  メイド🎀  ' } })
    const router = (await import('../index')).default
    const { loadBranding } = await import('@/shared/composables/useBranding')
    const { pageTitle } = await import('../routes')
    const { setDocumentTitle } = await import('../documentTitle')

    expect(pageTitle('home')).toBe('首页 - Community')
    await router.push('/about')
    expect(document.title).toBe('关于 - Community')
    await router.push('/login')
    expect(document.title).toBe('登录 - Community')

    await loadBranding()
    expect(pageTitle('home')).toBe('首页 - メイド🎀')
    expect(pageTitle('lists')).toBe('列表 - メイド🎀')
    expect(pageTitle('notifications')).toBe('通知 - メイド🎀')
    expect(pageTitle(undefined)).toBe('メイド🎀')
    expect(document.title).toBe('登录 - メイド🎀')

    await router.push('/explore')
    expect(router.currentRoute.value.name).not.toBe('explore')
    expect(document.title).toBe('欢迎 - メイド🎀')

    setDocumentTitle('私信')
    expect(document.title).toBe('私信 - メイド🎀')
    get.mockResolvedValue({ data: { title: '别的名字' } })
    await loadBranding(true)
    expect(document.title).toBe('私信 - 别的名字')
    expect(pageTitle('home')).toBe('首页 - 别的名字')
  })

  it('keeps Community when the instance title is empty', async () => {
    get.mockResolvedValue({ data: { title: '   ' } })
    const { loadBranding } = await import('@/shared/composables/useBranding')
    const { pageTitle } = await import('../routes')
    const { setDocumentTitle } = await import('../documentTitle')
    setDocumentTitle('首页')
    await loadBranding()
    expect(pageTitle('home')).toBe('首页 - Community')
    expect(document.title).toBe('首页 - Community')
  })
})
