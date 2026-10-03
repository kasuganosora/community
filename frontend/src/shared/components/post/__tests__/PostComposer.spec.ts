import { describe, expect, it, vi } from 'vitest'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia } from 'pinia'
import PostComposer from '../PostComposer.vue'
import EmojiPicker from '../EmojiPicker.vue'
import { postApi } from '@/domains/post/infrastructure/postApi'
import type { MastodonAttachment, MastodonStatus } from '@/shared/types/mastodon'

describe('PostComposer media upload', () => {
  it('blocks the keyboard shortcut until uploaded media can be included', async () => {
    let finishUpload!: (value: MastodonAttachment) => void
    vi.spyOn(postApi, 'uploadMedia').mockImplementation(() => new Promise(resolve => { finishUpload = resolve }))
    const create = vi.spyOn(postApi, 'create').mockResolvedValue({ id: 'new' } as MastodonStatus)
    const wrapper = shallowMount(PostComposer, { global: { plugins: [createPinia()], renderStubDefaultSlot: true } })
    wrapper.findComponent({ name: 'TTextarea' }).vm.$emit('update:modelValue', 'With a picture')
    const input = wrapper.find('input[type="file"]')
    Object.defineProperty(input.element, 'files', { value: [new File(['image'], 'test.png', { type: 'image/png' })] })
    await input.trigger('change')
    await wrapper.trigger('keydown', { key: 'Enter', ctrlKey: true })
    expect(create).not.toHaveBeenCalled()
    finishUpload({ id: 'media-1', type: 'image', url: '/image.png' } as MastodonAttachment)
    await flushPromises()
    await wrapper.trigger('keydown', { key: 'Enter', ctrlKey: true })
    expect(create).toHaveBeenCalledWith(expect.objectContaining({ media_ids: ['media-1'] }))
    wrapper.unmount()
  })
})


describe('PostComposer delete and repost', () => {
  const original = { id: 'old', visibility: 'private', content: '<p>Original</p>', spoiler_text: '', media_attachments: [] } as unknown as MastodonStatus

  it('discloses replacement and submits once without separately deleting the original', async () => {
    vi.spyOn(postApi, 'getSource').mockResolvedValue({ id: 'old', text: 'Original', spoiler_text: '' })
    let finish!: (status: MastodonStatus) => void
    const replace = vi.spyOn(postApi, 'replaceStatus').mockImplementation(() => new Promise(resolve => { finish = resolve }))
    const remove = vi.spyOn(postApi, 'deleteStatus')
    const create = vi.spyOn(postApi, 'create')
    const wrapper = shallowMount(PostComposer, { props: { editStatus: original }, global: { plugins: [createPinia()], renderStubDefaultSlot: true } })
    await flushPromises()
    expect(wrapper.get('[role="note"]').text()).toContain('帖子链接会改变')
    expect(wrapper.text()).toContain('删除原帖并重发')
    wrapper.findComponent({ name: 'TTextarea' }).vm.$emit('update:modelValue', 'Rewritten')
    await wrapper.trigger('keydown', { key: 'Enter', ctrlKey: true })
    await wrapper.trigger('keydown', { key: 'Enter', ctrlKey: true })
    expect(replace).toHaveBeenCalledTimes(1)
    expect(replace).toHaveBeenCalledWith('old', { status: 'Rewritten', spoiler_text: '', sensitive: false })
    expect(remove).not.toHaveBeenCalled()
    expect(create).not.toHaveBeenCalled()
    expect(wrapper.emitted('posted')).toBeUndefined()
    const replacement = { ...original, id: 'new' }
    finish(replacement)
    await flushPromises()
    expect(wrapper.emitted('posted')).toEqual([[replacement]])
    wrapper.unmount()
  })

  it('keeps the draft on replacement failure so it can be retried', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    vi.spyOn(postApi, 'getSource').mockResolvedValue({ id: 'old', text: 'Original', spoiler_text: '' })
    const replace = vi.spyOn(postApi, 'replaceStatus').mockRejectedValueOnce(new Error('Service unavailable')).mockResolvedValueOnce({ ...original, id: 'new' })
    const wrapper = shallowMount(PostComposer, { props: { editStatus: original }, global: { plugins: [createPinia()], renderStubDefaultSlot: true } })
    await flushPromises()
    wrapper.findComponent({ name: 'TTextarea' }).vm.$emit('update:modelValue', 'Keep my draft')
    await wrapper.trigger('keydown', { key: 'Enter', ctrlKey: true })
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('草稿已保留')
    expect(wrapper.findComponent({ name: 'TTextarea' }).props('modelValue')).toBe('Keep my draft')
    expect(wrapper.emitted('posted')).toBeUndefined()
    await wrapper.trigger('keydown', { key: 'Enter', ctrlKey: true })
    await flushPromises()
    expect(replace).toHaveBeenLastCalledWith('old', expect.objectContaining({ status: 'Keep my draft' }))
    expect(wrapper.emitted('posted')?.[0]?.[0]).toMatchObject({ id: 'new' })
    wrapper.unmount()
  })
  it('blocks replacement when the original source cannot be loaded', async () => {
    vi.spyOn(postApi, 'getSource').mockRejectedValue(new Error('Unavailable'))
    const replace = vi.spyOn(postApi, 'replaceStatus')
    const wrapper = shallowMount(PostComposer, { props: { editStatus: original }, global: { plugins: [createPinia()] } })
    await flushPromises()
    wrapper.findComponent({ name: 'TTextarea' }).vm.$emit('update:modelValue', 'Do not overwrite unavailable source')
    await wrapper.trigger('keydown', { key: 'Enter', ctrlKey: true })
    expect(replace).not.toHaveBeenCalled()
    expect(wrapper.get('[role="alert"]').text()).toContain('无法读取原帖内容')
    wrapper.unmount()
  })

})


describe('PostComposer custom emoji insertion', () => {
  it('replaces the selected range and restores the caret', async () => {
    const wrapper = shallowMount(PostComposer, { global: {
      plugins: [createPinia()],
      stubs: { TTextarea: { props: ['modelValue'], emits: ['update:modelValue'], template: '<textarea :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />' } },
    } })
    const input = wrapper.get('textarea')
    await input.setValue('left replace right')
    const element = input.element as HTMLTextAreaElement
    element.setSelectionRange(5, 12)
    await wrapper.get('button[aria-label="自定义表情"]').trigger('click')
    wrapper.findComponent(EmojiPicker).vm.$emit('select', { shortcode: 'wave' })
    await flushPromises()
    expect((wrapper.get('textarea').element as HTMLTextAreaElement).value).toBe('left :wave: right')
    expect(element.selectionStart).toBe(11)
    expect(element.selectionEnd).toBe(11)
    wrapper.unmount()
  })
})
