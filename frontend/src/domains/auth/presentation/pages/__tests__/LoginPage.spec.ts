import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const auth = vi.hoisted(() => ({
  isAuthenticated: false,
  error: null as string | null,
  loading: false,
  login: vi.fn(),
  loginTwoFactor: vi.fn(),
  loginWithPasskey: vi.fn(),
}))
const mockRoute = vi.hoisted(() => ({ query: {} as Record<string, string> }))
const mockGetInstance = vi.hoisted(() => vi.fn())

vi.mock('../../stores/authStore', () => ({ useAuthStore: () => auth }))
vi.mock('@/domains/instance/infrastructure/instanceApi', () => ({
  instanceApi: { getInstance: mockGetInstance },
}))
vi.mock('../../../infrastructure/captchaApi', () => ({
  captchaApi: { getConfig: vi.fn().mockResolvedValue({ enabled: false, provider: 'none' }) },
}))
vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn(), back: vi.fn() }),
  useRoute: () => mockRoute,
}))

import LoginPage from '../LoginPage.vue'

const stubs = {
  LanguagePicker: true,
  BrandMark: true,
  CaptchaField: true,
  't-alert': true,
  't-icon': true,
  't-button': { props: ['disabled', 'loading', 'type'], template: '<button :disabled="disabled" :type="type || \'button\'"><slot /></button>' },
  't-form': { template: '<form @submit.prevent="$emit(\'submit\')"><slot /></form>' },
  't-form-item': { template: '<div><slot /></div>' },
  't-input': {
    props: ['modelValue'],
    emits: ['update:modelValue'],
    template: '<input :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />',
  },
}

describe('LoginPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    auth.isAuthenticated = false
    auth.error = null
    auth.loading = false
    mockRoute.query = {}
    mockGetInstance.mockResolvedValue({ registrations: { enabled: false } })
  })

  it('displays the site name in the login heading when instance title is set', async () => {
    mockGetInstance.mockResolvedValue({ title: 'メイド🎀', registrations: { enabled: true } })
    const w = mount(LoginPage, { global: { stubs } })
    await flushPromises()
    expect(w.get('h1.login-title').text()).toBe('登录 メイド🎀')
    w.unmount()
  })

  it('falls back to the generic community heading when instance title is empty', async () => {
    mockGetInstance.mockResolvedValue({ title: '  ', registrations: { enabled: true } })
    const w = mount(LoginPage, { global: { stubs } })
    await flushPromises()
    expect(w.get('h1.login-title').text()).toBe('登录社区')
    w.unmount()
  })

  it('keeps the add-account heading when adding an account', async () => {
    auth.isAuthenticated = true
    mockRoute.query = { add: '1' }
    mockGetInstance.mockResolvedValue({ title: 'メイド🎀', registrations: { enabled: true } })
    const w = mount(LoginPage, { global: { stubs } })
    await flushPromises()
    expect(w.get('h1.login-title').text()).toBe('添加账户')
    w.unmount()
  })
})
