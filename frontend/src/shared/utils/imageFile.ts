import { t } from '@/shared/i18n'
/** Accept only images below `maxBytes` for avatar / header uploads. */
export function validateImageFile(file: { type: string; size: number }, maxBytes = 8 * 1024 * 1024): string | null {
  if (!file.type.startsWith('image/')) return t('shared.imageFile.s1')
  if (file.size > maxBytes) return t('shared.imageFile.s2', [Math.round(maxBytes / 1024 / 1024)])
  return null
}
