import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

const api = vi.hoisted(() => ({
  status: vi.fn(),
  setup: vi.fn(),
  confirm: vi.fn(),
  regenerateBackupCodes: vi.fn(),
  disable: vi.fn(),
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
})
