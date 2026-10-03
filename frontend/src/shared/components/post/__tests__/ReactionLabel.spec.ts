import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import ReactionLabel from '../ReactionLabel.vue'

describe('reaction notification label', () => {
  it('loads sensitive images only after explicit reveal and resets when changed', async () => {
    const reaction = { name: ':secret@remote.test:', count: 1, me: false, is_sensitive: true, url: 'https://remote.test/secret.png' }
    const wrapper = mount(ReactionLabel, { props: { reaction } })
    expect(wrapper.find('img').exists()).toBe(false)
    await wrapper.get('button').trigger('click')
    expect(wrapper.get('img').attributes('src')).toBe(reaction.url)
    await wrapper.setProps({ reaction: { ...reaction, name: ':other:' } })
    expect(wrapper.find('img').exists()).toBe(false)
  })
  it('renders unsafe URLs and Unicode as literal escaped labels', () => {
    const wrapper = mount(ReactionLabel, { props: { reaction: { name: '<img onerror=alert(1)>', count: 1, me: false, url: 'javascript:alert(1)' } } })
    expect(wrapper.find('img').exists()).toBe(false)
    expect(wrapper.text()).toBe('<img onerror=alert(1)>')
  })
})
