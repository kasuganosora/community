/** Accept only images below `maxBytes` for avatar / header uploads. */
export function validateImageFile(file: { type: string; size: number }, maxBytes = 8 * 1024 * 1024): string | null {
  if (!file.type.startsWith('image/')) return '请选择图片文件'
  if (file.size > maxBytes) return `图片不能超过 ${Math.round(maxBytes / 1024 / 1024)} MB`
  return null
}
