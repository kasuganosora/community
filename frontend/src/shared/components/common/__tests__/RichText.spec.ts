import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import RichText from '../RichText.vue'
describe('single sanitization rendering boundary', () => {
  it('keeps hostile markup inert while shortening links and preserving generated emoji', () => {
    const router = createRouter({ history: createMemoryHistory(), routes: [] })
    const wrapper = mount(RichText, { props: { html: '<script>alert(1)</script><a href="javascript:alert(2)">bad</a><a href="https://example.test/very/long/path/for/a/link">https://example.test/very/long/path/for/a/link</a><img src=x onerror=alert(3)> :safe:', emojis: [{ shortcode: 'safe', url: 'https://example.test/e.png', static_url: 'https://example.test/e.png', visible_in_picker: true }] }, global: { plugins: [router] } })
    expect(wrapper.find('script,[onerror],[href^="javascript:"]').exists()).toBe(false)
    expect(wrapper.findAll('img')).toHaveLength(1)
    expect(wrapper.get('img').attributes('alt')).toBe(':safe:')
    expect(wrapper.get('a[href^="https:"]').text()).toContain('…')
  })
})
