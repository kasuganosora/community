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
vi.mock('../../stores/authStore', () => ({ useAuthStore: () => auth }))
vi.mock('@/domains/instance/infrastructure/instanceApi', () => ({
  instanceApi: { getInstance: vi.fn().mockResolvedValue({ registrations: { enabled: false } }) },
}))
vi.mock('../../../infrastructure/captchaApi', () => ({
  captchaApi: { getConfig: vi.fn().mockResolvedValue({ enabled: false, provider: 'none' }) },
}))
vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn(), back: vi.fn() }),
  useRoute: () => ({ query: {} }),
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

describe('login two-factor challenge', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    auth.isAuthenticated = false
    auth.error = null
    auth.loading = false
  })

  it('accepts a security PIN in the same field as a TOTP or backup code', async () => {
    auth.login.mockResolvedValue({ twoFactorRequired: true, challenge: 'abc' })
    auth.loginTwoFactor.mockResolvedValue(undefined)
    const w = mount(LoginPage, { global: { stubs } })
    await flushPromises()
    const inputs = w.findAll('input')
    await inputs[0].setValue('qa')
    await inputs[1].setValue('secret')
    await w.get('form').trigger('submit')
    await flushPromises()
    const step = w.get('[data-testid="login-2fa"]')
    expect(step.text()).toContain('安全 PIN')
    expect(step.text()).toContain('备用码')
    await w.get('#login-2fa-code').setValue('cafe42')
    await step.trigger('submit')
    await flushPromises()
    expect(auth.loginTwoFactor).toHaveBeenCalledWith('abc', 'cafe42')
    w.unmount()
  })
})
