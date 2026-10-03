import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import EmojiAdminPage from '../EmojiAdminPage.vue'
import { emojiAdminApi, type AdminEmoji } from '../../../infrastructure/emojiAdminApi'

const remote: AdminEmoji = { reaction_role_ids: [], id: '42', shortcode: 'remote_cat', domain: 'other.test', url: '/cat.png', static_url: '/cat.png', aliases: ['cat'], category: 'animals', license: 'CC0', local_only: false, is_sensitive: false, visible_in_picker: true, disabled: false }
const options = { global: { stubs: { 't-icon': true } } }
beforeEach(() => { vi.spyOn(emojiAdminApi, 'roles').mockResolvedValue([]); vi.spyOn(emojiAdminApi, 'list').mockResolvedValue({ items: [remote], total: 1 }) })
afterEach(() => vi.restoreAllMocks())

describe('emoji administration', () => {
  it('does not overwrite remote metadata when changing local moderation preferences', async () => {
    const save = vi.spyOn(emojiAdminApi, 'save').mockResolvedValue(remote)
    const wrapper = mount(EmojiAdminPage, options)
    await flushPromises()
    const edit = wrapper.findAll('button').find(b => b.text() === '编辑')!
    await edit.trigger('click')
    const form = wrapper.get('form[aria-label="表情编辑"]')
    expect(form.find('input[type="file"]').exists()).toBe(false)
    await form.trigger('submit')
    await flushPromises()
    expect(save).toHaveBeenCalledWith('42', { disabled: false, visible_in_picker: true })
    wrapper.unmount()
  })
  it('keeps a new draft open when upload fails and prevents saving without an image', async () => {
    const upload = vi.spyOn(emojiAdminApi, 'upload').mockRejectedValue(new Error('图片格式无效'))
    const save = vi.spyOn(emojiAdminApi, 'save')
    const wrapper = mount(EmojiAdminPage, options)
    await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === '新建表情')!.trigger('click')
    const form = wrapper.get('form[aria-label="表情编辑"]')
    await form.get('input[pattern]').setValue('my_cat')
    const input = form.get('input[type="file"]')
    Object.defineProperty(input.element, 'files', { value: [new File(['bad'], 'cat.png', { type: 'image/png' })] })
    await input.trigger('change')
    await flushPromises()
    expect(upload).toHaveBeenCalledOnce()
    expect(wrapper.get('[role="alert"]').text()).toContain('图片格式无效')
    expect((form.get('input[pattern]').element as HTMLInputElement).value).toBe('my_cat')
    await form.trigger('submit')
    expect(save).not.toHaveBeenCalled()
    expect(wrapper.get('[role="alert"]').text()).toContain('请先上传')
    wrapper.unmount()
  })
  it('clears selection across origin changes so a bulk action cannot target a previous page', async () => {
    const bulk = vi.spyOn(emojiAdminApi, 'bulk')
    const wrapper = mount(EmojiAdminPage, options)
    await flushPromises()
    await wrapper.get('input[aria-label="选择 remote_cat"]').setValue(true)
    expect(wrapper.text()).toContain('本页全选（1）')
    await wrapper.get('select').setValue('remote')
    await flushPromises()
    expect(wrapper.text()).toContain('本页全选（0）')
    await wrapper.get('form.batch').trigger('submit')
    expect(bulk).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
