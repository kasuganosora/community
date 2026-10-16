import { t } from '@/shared/i18n'
export const MIN_PASSWORD_LENGTH = 8
export const MAX_DISPLAY_NAME_LENGTH = 30
export const MAX_NOTE_LENGTH = 500

export function validatePasswordChange(oldPw: string, newPw: string, confirm: string): string | null {
  if (!oldPw) return t('settings.validation.s1')
  if (newPw.length < MIN_PASSWORD_LENGTH) return t('settings.validation.s2', [MIN_PASSWORD_LENGTH])
  if (newPw !== confirm) return t('settings.validation.s3')
  if (newPw === oldPw) return t('settings.validation.s4')
  return null
}

export function validateProfile(displayName: string, note: string): string | null {
  if (Array.from(displayName).length > MAX_DISPLAY_NAME_LENGTH) return t('settings.validation.s5', [MAX_DISPLAY_NAME_LENGTH])
  if (Array.from(note).length > MAX_NOTE_LENGTH) return t('settings.validation.s6', [MAX_NOTE_LENGTH])
  return null
}

export { validateImageFile } from '@/shared/utils/imageFile'
