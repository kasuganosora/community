import { t } from '@/shared/i18n'
export interface AdminIdentity { is_admin?: boolean; admin_permissions?: string[] }
export interface AdminNavItem { path: string; title: string; description: string; icon: string; permission?: string; rootOnly?: boolean }
export interface AdminNavGroup { label: string; items: AdminNavItem[] }
const item = (path: string, title: string, icon: string, permission: string, description = ''): AdminNavItem => ({ path: '/admin' + path, title, icon, permission, description })
export const adminNavigation: AdminNavGroup[] = [
  { label: t('admin.navigation.s1'), items: [
    item('', t('admin.navigation.s2'), 'dashboard', 'dashboard', t('admin.navigation.s3')),
    item('/accounts', t('admin.navigation.s4'), 'usergroup', 'users'),
    item('/invites', t('admin.navigation.s5'), 'user-add', 'invites'),
    item('/roles', t('admin.navigation.s6'), 'secured', 'roles'),
    item('/emojis', t('admin.navigation.s7'), 'happy', 'emojis'),
    item('/decorations', t('admin.navigation.s8'), 'image', 'branding'),
    item('/instances', t('admin.navigation.s9'), 'internet', 'federation'),
    item('/jobs', t('admin.navigation.s10'), 'time', 'system'),
    item('/files', t('admin.navigation.s11'), 'file', 'media'),
    item('/announcements', t('admin.navigation.s12'), 'loudspeaker', 'announcements'),
    item('/advertisements', t('admin.navigation.s13'), 'image', 'branding'),
    item('/reports', t('admin.navigation.s14'), 'flag', 'reports'),
    item('/appeals', t('admin.navigation.s35'), 'chat', 'reports'),
    item('/discovery', t('admin.navigation.s15'), 'browse', 'settings', t('admin.navigation.s16')),
    item('/audit-logs', t('admin.navigation.s17'), 'history', 'audit'),
    item('/rules', t('admin.navigation.s18'), 'bulletpoint', 'rules'),
  ] },
  { label: t('admin.navigation.s19'), items: [
    item('/settings/general', t('admin.navigation.s20'), 'setting', 'settings'),
    item('/settings/branding', t('admin.navigation.s21'), 'palette', 'branding'),
    item('/moderation', t('admin.navigation.s1'), 'secured', 'settings', t('admin.navigation.s22')),
    item('/settings/moderation', t('admin.navigation.s23'), 'user-checked', 'settings'),
    item('/settings/email', t('admin.navigation.s24'), 'mail', 'settings'),
    item('/settings/storage', t('admin.navigation.s25'), 'cloud', 'settings'),
    item('/settings/security', t('admin.navigation.s26'), 'lock-on', 'settings'),
    item('/federation', t('admin.navigation.s27'), 'internet', 'federation'),
    item('/settings/external', t('admin.navigation.s28'), 'link', 'settings'),
    item('/webhooks', 'Webhook', 'code', 'webhooks'),
    item('/settings/performance', t('admin.navigation.s29'), 'chart', 'settings'),
  ] },
  { label: t('admin.navigation.s30'), items: [
    item('/settings/database', t('admin.navigation.s31'), 'data', 'system'),
    item('/system', t('admin.navigation.s32'), 'server', 'system'),
    { path: '/admin/migrations', title: t('admin.navigation.s33'), icon: 'data', description: t('admin.navigation.s34'), rootOnly: true },
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
