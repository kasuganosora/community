import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import ReviewsAdminPage from '../ReviewsAdminPage.vue'
import { reviewAdminApi, type ReviewJob } from '../../../infrastructure/reviewAdminApi'

const job: ReviewJob = { id: '8', account_id: '2', status_id: '3', acct: 'alice', display_name: 'Alice', text: 'a normal post', held: true, state: 'done', verdict: 'flag', reason: 'needs review', last_error: '', attempts: 1, created_at: '', actionable: true }
const result = { items: [job], has_more: false, configured: true, enabled: true }
let wrapper: VueWrapper
function button(text: string) { return wrapper.findAll('button').find(b => b.text() === text)! }
function render() { wrapper = mount(ReviewsAdminPage, { global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } }) }
beforeEach(() => {
  vi.spyOn(reviewAdminApi, 'list').mockResolvedValue(result)
  vi.spyOn(reviewAdminApi, 'act').mockResolvedValue()
  vi.spyOn(window, 'confirm').mockReturnValue(true)
})
afterEach(() => { wrapper?.unmount(); vi.restoreAllMocks() })

describe('new-account review workflow', () => {
  it('shows flagged posts and releases them only after confirmation', async () => {
    render(); await flushPromises()
    expect(wrapper.text()).toContain('需要人工复核')
    expect(wrapper.text()).toContain('a normal post')
    vi.mocked(window.confirm).mockReturnValue(false)
    await button('审核通过').trigger('click')
    expect(reviewAdminApi.act).not.toHaveBeenCalled()
    vi.mocked(window.confirm).mockReturnValue(true)
    vi.mocked(reviewAdminApi.list).mockResolvedValue({ ...result, items: [] })
    await button('审核通过').trigger('click'); await flushPromises()
    expect(reviewAdminApi.act).toHaveBeenCalledWith('8', 'approve')
    expect(wrapper.text()).toContain('没有待处理的审核记录')
  })
  it('keeps loaded posts visible after refresh failure', async () => {
    render(); await flushPromises()
    vi.mocked(reviewAdminApi.list).mockRejectedValue(new Error('refresh failed'))
    await button('刷新列表').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('a normal post')
    expect(wrapper.text()).toContain('refresh failed')
  })
  it('explains missing configuration and leaves manual approval available', async () => {
    vi.mocked(reviewAdminApi.list).mockResolvedValue({ ...result, configured: false })
    render(); await flushPromises()
    expect(wrapper.text()).toContain('尚未配置可用的审核服务')
    expect(button('重新审核').attributes('disabled')).toBeDefined()
    expect(button('审核通过').attributes('disabled')).toBeUndefined()
  })
})
