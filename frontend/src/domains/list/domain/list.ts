export const MAX_LIST_TITLE_LENGTH = 64

export function validateListTitle(title: string): string | null {
  const t = title.trim()
  if (!t) return '请输入列表名称'
  if (Array.from(t).length > MAX_LIST_TITLE_LENGTH) return `列表名称最多 ${MAX_LIST_TITLE_LENGTH} 个字`
  return null
}

export const REPLIES_POLICY_OPTIONS = [
  { value: 'list', label: '显示列表成员之间的回复' },
  { value: 'followed', label: '显示对我关注的人的回复' },
  { value: 'none', label: '不显示回复' },
] as const
