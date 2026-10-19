import { describe, expect, it } from 'vitest'
import { translateApiError } from '@/shared/i18n/apiErrors'
import { t } from '@/shared/i18n'

describe('translateApiError', () => {
  it('maps known backend messages case-insensitively', () => {
    expect(translateApiError('Record not found')).toBe(t('apiError.notFound'))
    expect(translateApiError('internal error')).toBe(t('apiError.internal'))
    expect(translateApiError('Registration is not open on this site')).toBe(t('apiError.registrationClosed'))
    expect(translateApiError('  already FAVOURITED ')).toBe(t('apiError.alreadyFavourited'))
  })
  it('still maps the older Chinese wording', () => {
    expect(translateApiError('本站暂不开放注册')).toBe(t('apiError.registrationClosed'))
    expect(translateApiError('群组不存在')).toBe(t('apiError.groupNotFound'))
  })
  it('maps the English wording of former Chinese server messages', () => {
    expect(translateApiError('Group not found')).toBe(t('apiError.groupNotFound'))
    expect(translateApiError('The captcha has expired, please get a new one')).toBe(t('apiError.captchaExpired'))
    expect(translateApiError('This group uses end-to-end encryption; use a Matrix client')).toBe(t('apiError.groupEncrypted'))
  })
  it('fills arguments from messages with a variable part', () => {
    expect(translateApiError('limit must be between 1 and 50')).toBe(t('apiError.listLimitRange', ['50']))
    expect(translateApiError('limit must be between 1 and 50')).toContain('50')
    expect(translateApiError('at most 3 decorations may be selected')).toBe(t('apiError.decorationLimit', ['3']))
    expect(translateApiError('API rate limit exceeded for /api/v1/statuses')).toBe(t('apiError.rateLimited'))
  })
  it('translates the detail of a validation failure', () => {
    expect(translateApiError('Validation failed: password is too short (minimum is 8 characters)')).toBe(t('apiError.passwordTooShort'))
    expect(translateApiError('Validation failed: something new')).toBe(t('apiError.validationFailed', ['something new']))
    expect(translateApiError('Validation failed: You have already voted on this poll')).toBe(t('apiError.alreadyVoted'))
    expect(translateApiError('Validation failed: The poll has already ended')).toBe(t('apiError.pollExpired'))
    expect(translateApiError('Validation failed: The chosen vote option does not exist')).toBe(t('apiError.invalidRequest'))
  })
  it('leaves unknown messages alone', () => {
    expect(translateApiError('something specific')).toBeNull()
    expect(translateApiError('')).toBeNull()
  })
})
