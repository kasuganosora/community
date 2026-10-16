import { beforeEach, describe, expect, it, vi } from 'vitest'

const get = vi.fn()
vi.mock('@/shared/api/client', () => ({ default: { get: (...args: unknown[]) => get(...args) } }))

describe('useBranding', () => {
  beforeEach(() => {
    vi.resetModules()
    get.mockReset()
    document.documentElement.removeAttribute('data-brand-info-image')
    document.documentElement.removeAttribute('data-brand-error-image')
    document.head.innerHTML = '<meta name="theme-color" content="#ffffff">'
  })

  it('loads branding from the instance document and applies it globally', async () => {
    get.mockResolvedValue({ data: { branding: { themeColor: '#86b300', infoImageUrl: 'https://x.example/i.png', entrancePageStyle: 'simple' } } })
    const { loadBranding } = await import('../useBranding')
    const b = await loadBranding()
    expect(get).toHaveBeenCalledWith('/api/v2/instance')
    expect(b.entrancePageStyle).toBe('simple')
    expect(b.showTimelineForVisitor).toBe(true)
    const root = document.documentElement
    expect(root.hasAttribute('data-brand-info-image')).toBe(true)
    expect(root.hasAttribute('data-brand-error-image')).toBe(false)
    expect(root.style.getPropertyValue('--brand-info-image')).toContain('https://x.example/i.png')
    expect(document.querySelector('meta[name="theme-color"]')?.getAttribute('content')).toBe('#86b300')
  })

  it('keeps defaults when the request fails and only fetches once', async () => {
    get.mockRejectedValue(new Error('offline'))
    const { loadBranding, DEFAULT_BRANDING } = await import('../useBranding')
    expect(await loadBranding()).toEqual(DEFAULT_BRANDING)
    get.mockResolvedValue({ data: {} })
    await loadBranding()
    await loadBranding()
    expect(get).toHaveBeenCalledTimes(2)
  })
})
