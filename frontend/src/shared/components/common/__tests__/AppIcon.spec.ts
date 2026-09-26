import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import AppIcon from '../AppIcon.vue'
import { normalizeIconSize, resolveIcon } from '../appIcons'

describe('normalizeIconSize', () => {
  it('turns numbers and numeric strings into px', () => {
    expect(normalizeIconSize(18)).toBe('18px')
    expect(normalizeIconSize('24')).toBe('24px')
  })
  it('passes named sizes and CSS lengths through', () => {
    expect(normalizeIconSize('large')).toBe('large')
    expect(normalizeIconSize('1.5em')).toBe('1.5em')
    expect(normalizeIconSize(undefined)).toBeUndefined()
  })
})

describe('AppIcon', () => {
  it('renders an inline svg without any network sprite', () => {
    const w = mount(AppIcon, { props: { name: 'heart', size: 18 } })
    const svg = w.find('svg')
    expect(svg.exists()).toBe(true)
    expect(svg.attributes('style')).toContain('font-size: 18px')
    expect(svg.attributes('aria-hidden')).toBe('true')
    expect(w.find('use').exists()).toBe(false)
  })

  it('knows every icon name used by the app', () => {
    for (const n of ['home', 'search', 'notification', 'mail', 'bookmark', 'bookmark-filled', 'user-circle',
      'heart', 'heart-filled', 'swap', 'chat', 'share', 'more', 'close', 'chevron-left', 'error-circle',
      'image', 'browse', 'browse-off', 'user-add', 'lock-on', 'link', 'chevron-up']) {
      expect(resolveIcon(n), n).toBeDefined()
    }
  })
})
