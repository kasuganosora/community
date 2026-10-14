import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import ListsPage from '../ListsPage.vue'
import { listApi } from '../../../infrastructure/listApi'
import { invalidateSessionData } from '@/shared/api/sessionScope'
import type { MastodonList } from '@/shared/types/mastodon'

vi.mock('tdesign-vue-next', () => ({ MessagePlugin: { success: vi.fn(), error: vi.fn() }, Loading: { template: '<span />' } }))
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: Error) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
const list = (title: string) => ({ id: title, title } as MastodonList)
function mountPage() {
  return shallowMount(ListsPage, { global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
}
afterEach(() => vi.restoreAllMocks())

describe('list loading and creation scope', () => {
  it('discards an old account list response after the account changes', async () => {
    const pending = deferred<MastodonList[]>()
    vi.spyOn(listApi, 'getLists').mockReturnValueOnce(pending.promise).mockResolvedValueOnce([list('current')])
    const wrapper = mountPage()
    invalidateSessionData()
    await flushPromises()
    pending.resolve([list('old private list')])
    await flushPromises()
    expect(wrapper.text()).toContain('current')
    expect(wrapper.text()).not.toContain('old private list')
    wrapper.unmount()
  })

  it('does not duplicate a pending create or append it to a new account', async () => {
    const pending = deferred<MastodonList>()
    vi.spyOn(listApi, 'getLists').mockResolvedValueOnce([]).mockResolvedValueOnce([list('current')])
    const create = vi.spyOn(listApi, 'createList').mockReturnValueOnce(pending.promise)
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('input').setValue('old private list')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.get('[type="submit"]').text()).toBe('创建中…')
    await wrapper.get('form').trigger('submit')
    expect(create).toHaveBeenCalledTimes(1)
    invalidateSessionData()
    await flushPromises()
    pending.resolve(list('old private list'))
    await flushPromises()
    expect(wrapper.text()).toContain('current')
    expect(wrapper.text()).not.toContain('old private list')
    expect(wrapper.get('input').element.value).toBe('')
    wrapper.unmount()
  })

  it('keeps the current request busy when a previous session request fails late', async () => {
    const old = deferred<MastodonList[]>(), current = deferred<MastodonList[]>()
    vi.spyOn(listApi, 'getLists').mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    const wrapper = mountPage()
    invalidateSessionData()
    await flushPromises()
    old.reject(new Error('old account failure'))
    await flushPromises()
    expect(wrapper.find('.page-loading').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('old account failure')
    current.resolve([list('current')])
    await flushPromises()
    expect(wrapper.find('.page-loading').exists()).toBe(false)
    expect(wrapper.text()).toContain('current')
    wrapper.unmount()
  })
})
