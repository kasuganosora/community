import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { listApi } from '../../../infrastructure/listApi'
import AddToListButton from '../AddToListButton.vue'

vi.mock('../../../infrastructure/listApi', () => ({ listApi: { getLists: vi.fn(), listsContaining: vi.fn(), addAccounts: vi.fn(), removeAccounts: vi.fn() } }))
const setup = () => mount(AddToListButton, { props: { accountId: 'alice' }, global: { stubs: { TDialog: { template: '<div><slot /></div>' }, TLoading: true, RouterLink: true } } })
let wrapper: ReturnType<typeof setup>
beforeEach(() => { vi.resetAllMocks(); vi.mocked(listApi.listsContaining).mockResolvedValue([]) })
afterEach(() => wrapper?.unmount())

it('shows a retryable error instead of an empty collection when the first load fails', async () => {
  vi.mocked(listApi.getLists).mockRejectedValueOnce(new Error('网络错误')).mockResolvedValueOnce([])
  wrapper = setup()
  await wrapper.get('button').trigger('click'); await flushPromises()
  expect(wrapper.get('[role="alert"]').text()).toContain('网络错误')
  expect(wrapper.text()).not.toContain('还没有列表')
  await wrapper.get('[role="alert"] button').trigger('click'); await flushPromises()
  expect(wrapper.find('[role="alert"]').exists()).toBe(false)
  expect(wrapper.text()).toContain('还没有列表')
})

it('shows pending immediately, blocks duplicate additions and ignores completion after account change', async () => {
  vi.mocked(listApi.getLists).mockResolvedValue([{ id: 'one', title: '朋友', replies_policy: 'list' }])
  let complete!: () => void
  vi.mocked(listApi.addAccounts).mockImplementation(() => new Promise(resolve => { complete = resolve }))
  wrapper = setup()
  await wrapper.get('button').trigger('click'); await flushPromises()
  const action = wrapper.get('.add-list__item button')
  await action.trigger('click'); await action.trigger('click')
  expect(action.text()).toBe('处理中…')
  expect(listApi.addAccounts).toHaveBeenCalledTimes(1)
  sessionEpoch.value++
  complete(); await flushPromises()
  expect(wrapper.find('.add-list__item').exists()).toBe(false)
  expect(wrapper.text()).not.toContain('处理中')
})
