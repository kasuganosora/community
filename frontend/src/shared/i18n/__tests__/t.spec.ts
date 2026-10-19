import { expect, it } from 'vitest'
import { t } from '@/shared/i18n'

it('accepts named arguments without throwing', () => {
  expect(() => t('settings.Connections.days', { count: 30 })).not.toThrow()
  expect(t('settings.Connections.days', { count: 30 })).toContain('30')
  expect(t('settings.Connections.attempts', { count: 2 })).toContain('2')
})
