import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { createPinia, setActivePinia } from 'pinia'
import AppealPage from '../AppealPage.vue'
import { appealApi, type Appeal } from '../../../infrastructure/appealApi'
import { authApi } from '../../../infrastructure/authApi'

vi.mock('../../../infrastructure/appealApi', () => ({ appealApi: { list: vi.fn(), create: vi.fn() } }))
vi.mock('../../../infrastructure/authApi', () => ({ authApi: { verifyCredentials: vi.fn(), logout: vi.fn() } }))

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
    vi.mocked(authApi.verifyCredentials).mockReset()
    vi.mocked(appealApi.list).mockReset()
    vi.mocked(appealApi.create).mockReset()
    vi.mocked(authApi.logout).mockReset()
  })

  it('shows the open appeal and does not offer a second submission', async () => {
    vi.mocked(authApi.verifyCredentials).mockRejectedValue(new Error('unauthorized'))
    vi.mocked(appealApi.list).mockResolvedValue([openAppeal])
    const page = mountPage()
    await flushPromises()
    expect(page.text()).toContain('当前申诉')
    expect(page.text()).toContain('this was a mistake')
    expect(page.text()).toContain('待处理')
    expect(page.find('textarea').exists()).toBe(false)
    page.unmount()
  })

  it('submits one appeal and then shows it', async () => {
    vi.mocked(authApi.verifyCredentials).mockRejectedValue(new Error('unauthorized'))
    vi.mocked(appealApi.list).mockResolvedValue([])
    vi.mocked(appealApi.create).mockResolvedValue(openAppeal)
    const page = mountPage()
    await flushPromises()
    expect(page.text()).toContain('还没有申诉')
    await page.get('textarea').setValue('  this was a mistake  ')
    await page.get('input').setValue('15')
    await page.get('form').trigger('submit')
    await flushPromises()
    expect(appealApi.create).toHaveBeenCalledWith('this was a mistake', '15')
    expect(page.text()).toContain('当前申诉')
    expect(page.find('textarea').exists()).toBe(false)
    page.unmount()
  })

  it('sends an account that can still load its profile back home', async () => {
    vi.mocked(authApi.verifyCredentials).mockResolvedValue({ id: '1' } as never)
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
    expect(appealApi.list).not.toHaveBeenCalled()
    page.unmount()
  })
})
