import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

const api = vi.hoisted(() => ({
  status: vi.fn(),
  setup: vi.fn(),
  confirm: vi.fn(),
  regenerateBackupCodes: vi.fn(),
  disable: vi.fn(),
  setSecurityPin: vi.fn(),
  removeSecurityPin: vi.fn(),
}))
vi.mock('../../../infrastructure/twoFactorApi', () => ({ twoFactorApi: api }))
vi.mock('qrcode-generator', () => ({
  default: () => ({ addData: vi.fn(), make: vi.fn(), createSvgTag: () => '<svg data-qr="1"></svg>' }),
}))

import TwoFactorSettings from '../TwoFactorSettings.vue'

describe('TwoFactorSettings', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('enables with password, QR + manual key, confirmation code and shows backup codes once', async () => {
    api.status.mockResolvedValueOnce({ enabled: false, enabled_at: null, backup_codes_remaining: 0 })
      .mockResolvedValue({ enabled: true, enabled_at: '2026-10-18T00:00:00Z', backup_codes_remaining: 10 })
    api.setup.mockResolvedValue({ secret: 'JBSWY3DPEHPK3PXP', otpauth_uri: 'otpauth://totp/x?secret=JBSWY3DPEHPK3PXP' })
    api.confirm.mockResolvedValue({ enabled: true, backup_codes: ['aaaaa-bbbbb', 'ccccc-ddddd'] })
    const w = mount(TwoFactorSettings)
    await flushPromises()
    await w.get('[data-testid="two-factor-enable"]').trigger('click')
    await w.get('#tf-setup-password').setValue('pw')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(api.setup).toHaveBeenCalledWith('pw')
    expect(w.get('[data-testid="two-factor-secret"]').text()).toBe('JBSW Y3DP EHPK 3PXP')
    expect(w.html()).toContain('data-qr')
    await w.get('#tf-confirm-code').setValue('123456')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(api.confirm).toHaveBeenCalledWith('123456')
    const codes = w.get('[data-testid="backup-codes"]')
    expect(codes.text()).toContain('aaaaa-bbbbb')
    expect(w.find('[data-testid="two-factor-secret"]').exists()).toBe(false)
    // dismissing hides the codes for good
    await codes.findAll('button').at(-1)!.trigger('click')
    expect(w.find('[data-testid="backup-codes"]').exists()).toBe(false)
    expect(w.text()).not.toContain('aaaaa-bbbbb')
  })

  it('disables only with password and code', async () => {
    api.status.mockResolvedValue({ enabled: true, enabled_at: '2026-10-18T00:00:00Z', backup_codes_remaining: 9 })
    api.disable.mockRejectedValueOnce(Object.assign(new Error('x'), { apiMessage: 'invalid_two_factor_code' })).mockResolvedValue(undefined)
    const w = mount(TwoFactorSettings)
    await flushPromises()
    const disableButton = w.findAll('button').find(b => b.classes('two-factor__danger'))!
    await disableButton.trigger('click')
    await w.get('#tf-act-password').setValue('pw')
    await w.get('#tf-act-code').setValue('000000')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(w.find('[role="alert"]').exists()).toBe(true)
    await w.get('#tf-act-code').setValue('654321')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(api.disable).toHaveBeenLastCalledWith('pw', '654321')
  })

  it('sets, changes and removes a security PIN without turning two-factor off', async () => {
    const on = { enabled: true, enabled_at: '2026-10-18T00:00:00Z', backup_codes_remaining: 9, security_pin_set: false }
    const set = { ...on, backup_codes_remaining: 8, security_pin_set: true }
    api.status.mockResolvedValueOnce(on).mockResolvedValueOnce(set).mockResolvedValueOnce(set)
      .mockResolvedValue({ ...on, security_pin_set: false })
    api.setSecurityPin.mockResolvedValue({ security_pin_set: true })
    api.removeSecurityPin.mockResolvedValue(undefined)
    const w = mount(TwoFactorSettings)
    await flushPromises()
    expect(w.get('[data-testid="security-pin"]').text()).toContain('尚未设置安全 PIN')
    expect(w.find('.two-factor__danger').exists()).toBe(true)

    await w.get('[data-testid="security-pin-set"]').trigger('click')
    await w.get('#tf-pin-password').setValue('pw')
    await w.get('#tf-pin-code').setValue('123456')
    await w.get('#tf-pin-new').setValue('nope')
    await w.get('#tf-pin-confirm').setValue('nope')
    await w.get('[data-testid="security-pin-form"]').trigger('submit')
    expect(api.setSecurityPin).not.toHaveBeenCalled()
    expect(w.get('[role="alert"]').text()).toContain('6–32')

    await w.get('#tf-pin-new').setValue('cafe42')
    await w.get('#tf-pin-confirm').setValue('other1')
    await w.get('[data-testid="security-pin-form"]').trigger('submit')
    expect(api.setSecurityPin).not.toHaveBeenCalled()

    await w.get('#tf-pin-confirm').setValue('cafe42')
    await w.get('[data-testid="security-pin-form"]').trigger('submit')
    await flushPromises()
    expect(api.setSecurityPin).toHaveBeenCalledWith('pw', '123456', 'cafe42')
    expect(w.get('[data-testid="security-pin"]').text()).toContain('已设置安全 PIN')
    expect(w.find('.two-factor__danger').text()).toContain('关闭两步验证')

    await w.get('[data-testid="security-pin-change"]').trigger('click')
    await w.get('#tf-pin-password').setValue('pw')
    await w.get('#tf-pin-code').setValue('cafe42')
    await w.get('#tf-pin-new').setValue('pin9001')
    await w.get('#tf-pin-confirm').setValue('pin9001')
    await w.get('[data-testid="security-pin-form"]').trigger('submit')
    await flushPromises()
    expect(api.setSecurityPin).toHaveBeenLastCalledWith('pw', 'cafe42', 'pin9001')

    await w.get('[data-testid="security-pin-remove"]').trigger('click')
    await w.get('#tf-pin-remove-password').setValue('pw')
    await w.get('#tf-pin-remove-code').setValue('pin9001')
    await w.get('[data-testid="security-pin-remove-form"]').trigger('submit')
    await flushPromises()
    expect(api.removeSecurityPin).toHaveBeenCalledWith('pw', 'pin9001')
    expect(api.disable).not.toHaveBeenCalled()
    expect(w.get('[data-testid="security-pin"]').text()).toContain('尚未设置安全 PIN')
    expect(w.find('.two-factor__danger').text()).toContain('关闭两步验证')
  })
})
