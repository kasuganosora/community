import { t } from '@/shared/i18n'

/**
 * Backend error strings (the API's {"error": "..."} values) that have a
 * localized counterpart under "apiError.*". Matched case-insensitively on the
 * whole message; anything unmapped is shown as the server sent it.
 */
const API_ERRORS: Record<string, string> = {
  'internal error': 'internal',
  'internal server error': 'internal',
  'server_error': 'internal',
  'operation failed': 'internal',
  'forbidden': 'forbidden',
  'has no permission': 'forbidden',
  'unauthorized': 'unauthorized',
  'authentication required': 'unauthorized',
  'invalid token': 'unauthorized',
  'token error': 'unauthorized',
  'not found': 'notFound',
  'record not found': 'notFound',
  'account not found': 'accountNotFound',
  'status not found': 'statusNotFound',
  'notification not found': 'notificationNotFound',
  'filter not found': 'filterNotFound',
  'invalid request': 'invalidRequest',
  'invalid request body': 'invalidRequest',
  '请求参数无效': 'invalidRequest',
  'invalid id': 'invalidRequest',
  'id must be a number': 'invalidRequest',
  'invalid account id': 'invalidRequest',
  'invalid status id': 'invalidRequest',
  'invalid group id': 'invalidRequest',
  'invalid username or password': 'badCredentials',
  'invalid_grant': 'badCredentials',
  'user status abnormal': 'userAbnormal',
  'passkey verification failed': 'passkeyFailed',
  'search too long': 'searchTooLong',
  'invalid domain': 'invalidDomain',
  'setting is managed by deployment': 'managedByDeployment',
  'matrix bridge is not running': 'matrixOff',
  'matrix bridge is not enabled': 'matrixOff',
  '验证码已失效，请换一个': 'captchaExpired',
  '验证码暂时不可用，请稍后再试': 'captchaUnavailable',
  '验证码没有通过，请再试一次': 'captchaFailed',
  '验证码不正确，请再试一次': 'captchaFailed',
  '请先看完五个字符': 'captchaWatch',
  '请稍等，页面还在确认': 'captchaWait',
  '换得太频繁，请稍后再试': 'captchaTooFast',
  '本站暂不开放注册': 'registrationClosed',
  '至少需要一个关键词': 'keywordRequired',
  '关键词不能为空': 'keywordRequired',
  '标题不能为空': 'titleRequired',
  '不支持的过滤范围': 'filterContext',
  '不支持的过滤动作': 'filterAction',
  '请填写不超过 500 字的管理原因': 'reasonTooLong',
  '话题榜已更新，请刷新': 'trendsChanged',
  '推荐已更新，请刷新后继续': 'trendsChanged',
  '当前索引版本已变化，请刷新页面后重新确认': 'trendsChanged',
  '话题暂时不可用，请稍后重试': 'temporarilyUnavailable',
  '推荐管理暂时不可用，请稍后重试': 'temporarilyUnavailable',
  '推荐暂时不可用，请稍后重试': 'temporarilyUnavailable',
  '规则不存在': 'ruleNotFound',
  '规则目标不存在或格式无效': 'ruleNotFound',
  '无效的规则编号': 'ruleNotFound',
  '无效的推荐管理规则': 'ruleNotFound',
  '缺少要批准的索引版本': 'indexNotReady',
  '推荐索引尚未构建完成，暂时没有可批准的版本': 'indexNotReady',
  '只能引用公开或不公开的帖子': 'quoteVisibility'
}

/** Localized text for a backend error message, or null when unmapped. */
export function translateApiError(message: string | null | undefined): string | null {
  if (!message) return null
  const key = API_ERRORS[message.trim().toLowerCase()]
  return key ? t(`apiError.${key}`) : null
}
