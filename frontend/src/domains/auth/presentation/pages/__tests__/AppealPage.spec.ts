import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { createPinia, setActivePinia } from 'pinia'
import AppealPage from '../AppealPage.vue'
import { appealApi, type Appeal } from '../../../infrastructure/appealApi'
import { clearAppealSession, isAppealSession, markAppealSession } from '@/shared/api/appealSession'

vi.mock('../../../infrastructure/appealApi', () => ({ appealApi: { list: vi.fn(), context: vi.fn(), create: vi.fn() } }))
vi.mock('../../stores/authStore', () => ({ useAuthStore: () => ({ fetchCurrentUser: vi.fn(async () => true), logout: vi.fn() }) }))

const openAppeal: Appeal = {
  id: '8', account_id: '9', status_id: '15', text: 'this was a mistake', state: 'open', created_at: '2026-01-02T03:04:05Z',
}

function mountPage() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/appeal', name: 'appeal', component: AppealPage },
      { path: '/', name: 'home', component: { template: '<div>home</div>' } },
    ],
  })
  return mount(AppealPage, {
    global: {
      plugins: [router, createPinia()],
      stubs: { BrandMark: true, LanguagePicker: true },
    },
  })
}

describe('suspended account appeal page', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    clearAppealSession()
    vi.mocked(appealApi.context).mockReset()
    vi.mocked(appealApi.context).mockResolvedValue({ items: [], suspended: true, suspension: { status_id: '15', can_appeal: true } })
    vi.mocked(appealApi.list).mockReset()
    vi.mocked(appealApi.create).mockReset()

  })

  it('shows the open appeal and does not offer a second submission', async () => {
    vi.mocked(appealApi.context).mockResolvedValue({ items: [openAppeal], suspended: true, suspension: { status_id: '15', can_appeal: false } })
    const page = mountPage()
    await flushPromises()
    expect(page.text()).toContain('当前申诉')
    expect(page.text()).toContain('this was a mistake')
    expect(page.text()).toContain('待处理')
    expect(page.find('textarea').exists()).toBe(false)
    page.unmount()
  })

  it('submits one appeal and then shows it', async () => {
    vi.mocked(appealApi.list).mockResolvedValue([])
    vi.mocked(appealApi.create).mockResolvedValue(openAppeal)
    const page = mountPage()
    await flushPromises()
    expect(page.text()).toContain('还没有申诉')
    await page.get('textarea').setValue('  this was a mistake  ')
    await page.get('form').trigger('submit')
    await flushPromises()
    expect(appealApi.create).toHaveBeenCalledWith('this was a mistake', '15')
    expect(page.text()).toContain('当前申诉')
    expect(page.find('textarea').exists()).toBe(false)
    page.unmount()
  })

  it('sends an account that can still load its profile back home', async () => {
    markAppealSession()
    vi.mocked(appealApi.context).mockResolvedValue({ items: [], suspended: false, suspension: null })
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/appeal', name: 'appeal', component: AppealPage },
        { path: '/', name: 'home', component: { template: '<div>home</div>' } },
      ],
    })
    await router.push('/appeal')
    const page = mount(AppealPage, { global: { plugins: [router, createPinia()], stubs: { BrandMark: true, LanguagePicker: true } } })
    await flushPromises()
    expect(router.currentRoute.value.name).toBe('home')
    expect(isAppealSession()).toBe(false)
    page.unmount()
  })
  it('preserves the submission error and the draft when refreshing the case', async () => {
    vi.mocked(appealApi.create).mockRejectedValue(new Error('Submission failed'))
    const page = mountPage()
    await flushPromises()
    await page.get('textarea').setValue('please review my post')
    await page.get('form').trigger('submit')
    await flushPromises()
    expect(page.get('[role="alert"]').text()).toBe('Submission failed')
    expect(page.get('textarea').element.value).toBe('please review my post')
    page.unmount()
  })

  it('does not offer a second appeal for a closed case', async () => {
    vi.mocked(appealApi.context).mockResolvedValue({ items: [{ ...openAppeal, state: 'rejected' }], suspended: true, suspension: { status_id: '15', can_appeal: false } })
    const page = mountPage()
    await flushPromises()
    expect(page.find('textarea').exists()).toBe(false)
    expect(page.text()).toContain('此次申诉已处理')
    page.unmount()
  })

  it('offers retry after an initial failure without exposing an unusable form', async () => {
    vi.mocked(appealApi.context).mockRejectedValueOnce(new Error('network unavailable'))
    const page = mountPage()
    await flushPromises()
    expect(page.find('textarea').exists()).toBe(false)
    expect(page.text()).toContain('network unavailable')
    await page.findAll('button').find(b => b.text() === '刷新申诉状态')!.trigger('click')
    await flushPromises()
    expect(page.find('textarea').exists()).toBe(true)
    page.unmount()
  })

})
