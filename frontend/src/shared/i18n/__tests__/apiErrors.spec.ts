import { describe, expect, it } from 'vitest'
import { translateApiError } from '@/shared/i18n/apiErrors'
import { t } from '@/shared/i18n'

describe('translateApiError', () => {
  it('maps known backend messages case-insensitively', () => {
    expect(translateApiError('Record not found')).toBe(t('apiError.notFound'))
    expect(translateApiError('internal error')).toBe(t('apiError.internal'))
    expect(translateApiError('本站暂不开放注册')).toBe(t('apiError.registrationClosed'))
  })
  it('leaves unknown messages alone', () => {
    expect(translateApiError('something specific')).toBeNull()
    expect(translateApiError('')).toBeNull()
  })
})
