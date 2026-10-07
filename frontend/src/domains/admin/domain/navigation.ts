export interface AdminIdentity { is_admin?: boolean; admin_permissions?: string[] }
export interface AdminNavItem { path: string; title: string; description: string; icon: string; permission?: string; rootOnly?: boolean }
export interface AdminNavGroup { label: string; items: AdminNavItem[] }
const item = (path: string, title: string, icon: string, permission: string, description = ''): AdminNavItem => ({ path: '/admin' + path, title, icon, permission, description })
export const adminNavigation: AdminNavGroup[] = [
  { label: '管理', items: [
    item('', '概览', 'dashboard', 'dashboard', '了解社区近况，处理需要关注的事务。'),
    item('/accounts', '用户管理', 'usergroup', 'users'),
    item('/invites', '邀请', 'user-add', 'invites'),
    item('/roles', '角色', 'secured', 'roles'),
    item('/emojis', '自定义表情', 'happy', 'emojis'),
    item('/decorations', '头像装饰', 'image', 'branding'),
    item('/instances', '联合', 'internet', 'federation'),
    item('/jobs', '作业队列（含联邦投递）', 'time', 'system'),
    item('/files', '文件', 'file', 'media'),
    item('/announcements', '公告', 'notification', 'announcements'),
    item('/advertisements', '广告', 'image', 'branding'),
    item('/reports', '举报与内容', 'flag', 'reports'),
    item('/audit-logs', '审查日志', 'history', 'audit'),
    item('/rules', '社区规则', 'bulletpoint', 'rules'),
  ] },
  { label: '设置', items: [
    item('/settings/general', '常规', 'setting', 'settings'),
    item('/settings/branding', '品牌', 'palette', 'branding'),
    item('/settings/moderation', '注册管理', 'user-checked', 'settings'),
    item('/settings/email', '邮件服务器', 'mail', 'settings'),
    item('/settings/storage', '对象存储', 'cloud', 'settings'),
    item('/settings/security', '安全', 'lock-on', 'settings'),
    item('/federation', '中继', 'internet', 'federation'),
    item('/settings/external', '外部服务', 'link', 'settings'),
    item('/webhooks', 'Webhook', 'code', 'webhooks'),
    item('/settings/performance', '性能', 'chart', 'settings'),
  ] },
  { label: '关于与维护', items: [
    item('/settings/database', '数据库', 'data', 'system'),
    item('/system', 'Matrix 服务', 'server', 'system'),
    { path: '/admin/migrations', title: '数据迁移', icon: 'data', description: '预检并导入同域名的完整站点备份。', rootOnly: true },
  ] },
]
export function canAccessAdmin(identity: AdminIdentity | null | undefined, permission?: string, rootOnly = false): boolean {
  if (identity?.is_admin === true) return true
  if (!identity || rootOnly || !permission) return false
  return identity.admin_permissions?.includes(permission) === true
}
export function visibleAdminNavigation(identity: AdminIdentity | null | undefined): AdminNavGroup[] {
  return adminNavigation.map(group => ({ ...group, items: group.items.filter(i => canAccessAdmin(identity, i.permission, i.rootOnly)) })).filter(g => g.items.length)
}
export function adminEntry(identity: AdminIdentity | null | undefined): string | undefined {
  return visibleAdminNavigation(identity)[0]?.items[0]?.path
}
export function adminSection(path: string): AdminNavItem {
  return adminNavigation.flatMap(group => group.items).find(i => i.path === path.replace(/\/$/, '')) ?? adminNavigation[0]!.items[0]!
}
