export const MIN_PASSWORD_LENGTH = 8
export const MAX_DISPLAY_NAME_LENGTH = 30
export const MAX_NOTE_LENGTH = 500

export function validatePasswordChange(oldPw: string, newPw: string, confirm: string): string | null {
  if (!oldPw) return '请输入当前密码'
  if (newPw.length < MIN_PASSWORD_LENGTH) return `新密码至少需要 ${MIN_PASSWORD_LENGTH} 个字符`
  if (newPw !== confirm) return '两次输入的新密码不一致'
  if (newPw === oldPw) return '新密码不能与当前密码相同'
  return null
}

export function validateProfile(displayName: string, note: string): string | null {
  if (Array.from(displayName).length > MAX_DISPLAY_NAME_LENGTH) return `昵称最多 ${MAX_DISPLAY_NAME_LENGTH} 个字`
  if (Array.from(note).length > MAX_NOTE_LENGTH) return `简介最多 ${MAX_NOTE_LENGTH} 个字`
  return null
}

export { validateImageFile } from '@/shared/utils/imageFile'
