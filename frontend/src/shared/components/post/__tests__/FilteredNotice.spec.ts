import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import FilteredNotice from '../FilteredNotice.vue'

describe('Mastodon stream and HTTP filter results', () => {
  it('withholds filtered body/media until explicit reveal, and resets reveal when a new rule arrives', async () => {
    const wrapper = mount(FilteredNotice, {
      props: { marks: [{ filter: { id: 'one', filter_action: 'warn', context: ['home'] }, keyword_matches: ['spoiler'] }] },
      slots: { default: '<div>secret body<img src="https://example.test/private.png" /></div>' },
    })
    expect(wrapper.find('img').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('secret body')
    await wrapper.get('button').trigger('click')
    expect(wrapper.find('img').exists()).toBe(true)
    await wrapper.setProps({ marks: [{ filter: { id: 'two', filter_action: 'hide', context: ['home'] } }] })
    expect(wrapper.find('img').exists()).toBe(false)
    expect(wrapper.text()).toContain('已被隐藏')
    wrapper.unmount()
  })
  it('handles both legacy string marks and mixed wrapper/original results with hide taking priority', () => {
    const wrapper = mount(FilteredNotice, {
      props: { marks: ['warn', { filter: { filter_action: 'hide' } }] },
      slots: { default: 'hidden original body' },
    })
    expect(wrapper.text()).toContain('已被隐藏')
    expect(wrapper.text()).not.toContain('hidden original body')
    wrapper.unmount()
  })
})
