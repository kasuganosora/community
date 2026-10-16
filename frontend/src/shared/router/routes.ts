import { adminNavigation } from '@/domains/admin/domain/navigation'
import type { RouteRecordRaw } from 'vue-router'
import { lazyRoute } from './lazyRoute'

// Define route names as constants for better maintainability
export const RouteNames = {
  LANDING: 'landing',
  LOGIN: 'login',
  REGISTER: 'register',
  HOME: 'home',
  EXPLORE: 'explore',
  TOPICS: 'topics',
  NOTIFICATIONS: 'notifications',
  MESSAGES: 'messages',
  CHAT_CONVERSATION: 'chat-conversation',
  GROUPS: 'groups',
  GROUP: 'group',
  BOOKMARKS: 'bookmarks',
  SEARCH: 'search',
  PROFILE: 'profile',
  POST_DETAIL: 'post-detail',
  TAG: 'tag',
  FOLLOWERS: 'followers',
  FOLLOWING: 'following',
  LISTS: 'lists',
  LIST_TIMELINE: 'list-timeline',
  SETTINGS: 'settings',
  ADMIN_OVERVIEW: 'admin-overview',
  ADMIN_ACCOUNTS: 'admin-accounts',
  ADMIN_REPORTS: 'admin-reports',
  ADMIN_FEDERATION: 'admin-federation',
  ADMIN_SYSTEM: 'admin-system',
  ADMIN_DISCOVERY: 'admin-discovery',
  ADMIN_SETTINGS: 'admin-settings',
  ADMIN_EMOJIS: 'admin-emojis',
  ADMIN_MIGRATIONS: 'admin-migrations',
  PERSONAL_DATA: 'personal-data',
  ABOUT: 'about',
  ANNOUNCEMENTS: 'announcements',
  ADMIN_INVITES: 'admin-invites',
  ADMIN_ROLES: 'admin-roles',
  ADMIN_DECORATIONS: 'admin-decorations',
  ADMIN_ADVERTISEMENTS: 'admin-advertisements',
  ADMIN_INSTANCES: 'admin-instances',
  ADMIN_JOBS: 'admin-jobs',
  ADMIN_FILES: 'admin-files',
  ADMIN_ANNOUNCEMENTS: 'admin-announcements',
  ADMIN_AUDIT: 'admin-audit',
  ADMIN_WEBHOOKS: 'admin-webhooks',
  ADMIN_RULES: 'admin-rules',
  ADMIN_MODERATION: 'admin-moderation',
} as const

/** Union of all route name string literals */
export type RouteName = (typeof RouteNames)[keyof typeof RouteNames]

// Preserve page instances when sibling routes use the same view with new props.
const DataTransferPageRoute = lazyRoute(() => import('@/domains/data/presentation/pages/DataTransferPage.vue'), pageHeading)
const GroupsPageRoute = lazyRoute(() => import('@/domains/group/presentation/pages/GroupsPage.vue'), pageHeading)
const FollowListPageRoute = lazyRoute(() => import('@/domains/user/presentation/pages/FollowListPage.vue'), pageHeading)
const BrandAssetsAdminPageRoute = lazyRoute(() => import('@/domains/admin/presentation/pages/BrandAssetsAdminPage.vue'), pageHeading)
const SettingsCenterPageRoute = lazyRoute(() => import('@/domains/admin/presentation/pages/SettingsCenterPage.vue'), pageHeading)

// Route groups for better code splitting
const AuthRoutes: RouteRecordRaw[] = [
  {
    path: '/',
    name: RouteNames.LANDING,
    component: lazyRoute(() => import('@/domains/auth/presentation/pages/LandingPage.vue'), pageHeading),
    meta: { guestOnly: true },
  },
  {
    path: '/login',
    name: RouteNames.LOGIN,
    component: lazyRoute(() => import('@/domains/auth/presentation/pages/LoginPage.vue'), pageHeading),
    meta: { guestOnly: true },
  },
  {
    path: '/register',
    name: RouteNames.REGISTER,
    component: lazyRoute(() => import('@/domains/auth/presentation/pages/RegisterPage.vue'), pageHeading),
    meta: { guestOnly: true },
  },
]

// Public (no auth, no guest-only redirect): instance info, rules and terms.
const PublicRoutes: RouteRecordRaw[] = [
  {
    path: '/about',
    name: RouteNames.ABOUT,
    component: lazyRoute(() => import('@/domains/instance/presentation/pages/AboutPage.vue'), pageHeading),
  },
]

const MainRoutes: RouteRecordRaw[] = [
  { path: 'explore/topics', name: RouteNames.TOPICS, component: lazyRoute(() => import('@/domains/discovery/presentation/pages/TopicsPage.vue'), pageHeading) },
  { path: 'announcements', name: RouteNames.ANNOUNCEMENTS, component: lazyRoute(() => import('@/domains/admin/presentation/pages/AnnouncementsPage.vue'), pageHeading) },
  { path: 'settings/data', name: RouteNames.PERSONAL_DATA, component: DataTransferPageRoute, props: { scope: 'personal' } },
  {
    path: '',
    name: RouteNames.HOME,
    component: lazyRoute(() => import('@/domains/timeline/presentation/pages/HomePage.vue'), pageHeading),
  },
  {
    path: 'explore',
    name: RouteNames.EXPLORE,
    component: lazyRoute(() => import('@/domains/timeline/presentation/pages/ExplorePage.vue'), pageHeading),
  },
  {
    path: 'notifications',
    name: RouteNames.NOTIFICATIONS,
    component: lazyRoute(() => import('@/domains/notification/presentation/pages/NotificationsPage.vue'), pageHeading),
  },
  {
    path: 'messages',
    name: RouteNames.MESSAGES,
    component: lazyRoute(() => import('@/domains/message/presentation/pages/MessagesPage.vue'), pageHeading),
  },
  { path: 'groups', name: RouteNames.GROUPS, component: GroupsPageRoute },
  { path: 'groups/:groupId', name: RouteNames.GROUP, component: GroupsPageRoute },
  {
    path: 'messages/groups',
    redirect: to => ({ name: RouteNames.GROUPS, query: to.query, hash: to.hash }),
  },
  {
    path: 'messages/groups/:groupId',
    redirect: to => ({ name: RouteNames.GROUP, params: { groupId: to.params.groupId }, query: to.query, hash: to.hash }),
  },
  {
    path: 'messages/:conversationId',
    name: RouteNames.CHAT_CONVERSATION,
    component: lazyRoute(() => import('@/domains/message/presentation/pages/ChatThreadPage.vue'), pageHeading),
  },
  {
    path: 'bookmarks',
    name: RouteNames.BOOKMARKS,
    component: lazyRoute(() => import('@/domains/bookmark/presentation/pages/BookmarksPage.vue'), pageHeading),
  },
  {
    path: 'search',
    name: RouteNames.SEARCH,
    component: lazyRoute(() => import('@/domains/trending/presentation/pages/SearchResultsPage.vue'), pageHeading),
  },
  {
    path: 'settings',
    name: RouteNames.SETTINGS,
    component: lazyRoute(() => import('@/domains/settings/presentation/pages/SettingsPage.vue'), pageHeading),
  },
  {
    path: '@:username',
    name: RouteNames.PROFILE,
    component: lazyRoute(() => import('@/domains/user/presentation/pages/ProfilePage.vue'), pageHeading),
    props: true,
  },
  {
    path: '@:username/followers',
    name: RouteNames.FOLLOWERS,
    component: FollowListPageRoute,
    props: { kind: 'followers' },
  },
  {
    path: '@:username/following',
    name: RouteNames.FOLLOWING,
    component: FollowListPageRoute,
    props: { kind: 'following' },
  },
  {
    path: 'tags/:tag',
    name: RouteNames.TAG,
    component: lazyRoute(() => import('@/domains/timeline/presentation/pages/TagTimelinePage.vue'), pageHeading),
  },
  {
    path: 'lists',
    name: RouteNames.LISTS,
    component: lazyRoute(() => import('@/domains/list/presentation/pages/ListsPage.vue'), pageHeading),
  },
  {
    path: 'lists/:id',
    name: RouteNames.LIST_TIMELINE,
    component: lazyRoute(() => import('@/domains/list/presentation/pages/ListTimelinePage.vue'), pageHeading),
  },
  {
    path: '@:username/status/:statusId',
    alias: '@:username/:statusId(\\d+)',
    name: RouteNames.POST_DETAIL,
    component: lazyRoute(() => import('@/domains/post/presentation/pages/PostDetailPage.vue'), pageHeading),
    props: true,
  },
]

export const routes: RouteRecordRaw[] = [
  ...AuthRoutes,
  ...PublicRoutes,
  {
    path: '/',
    component: lazyRoute(() => import('@/shared/components/layout/AppLayout.vue'), pageHeading),
    meta: { requiresAuth: true },
    children: MainRoutes,
  },
  {
    path: '/admin',
    component: lazyRoute(() => import('@/domains/admin/presentation/layouts/AdminLayout.vue'), pageHeading),
    meta: { requiresAuth: true, requiresAdmin: true },
    children: [
      { path: 'discovery', name: RouteNames.ADMIN_DISCOVERY, component: lazyRoute(() => import('@/domains/admin/presentation/pages/DiscoveryAdminPage.vue'), pageHeading), meta: { adminPermission: 'settings' } },
      { path: '', name: RouteNames.ADMIN_OVERVIEW, component: lazyRoute(() => import('@/domains/admin/presentation/pages/OverviewAdminPage.vue'), pageHeading), meta: { adminPermission: 'dashboard' } },
      { path: 'accounts', name: RouteNames.ADMIN_ACCOUNTS, component: lazyRoute(() => import('@/domains/admin/presentation/pages/AccountsAdminPage.vue'), pageHeading), meta: { adminPermission: 'users' } },
      { path: 'reports', name: RouteNames.ADMIN_REPORTS, component: lazyRoute(() => import('@/domains/admin/presentation/pages/ReportsAdminPage.vue'), pageHeading), meta: { adminPermission: 'reports' } },
      { path: 'federation', name: RouteNames.ADMIN_FEDERATION, component: lazyRoute(() => import('@/domains/admin/presentation/pages/FederationAdminPage.vue'), pageHeading), meta: { adminPermission: 'federation' } },
      { path: 'system', name: RouteNames.ADMIN_SYSTEM, component: lazyRoute(() => import('@/domains/admin/presentation/pages/SystemAdminPage.vue'), pageHeading), meta: { adminPermission: 'system' } },
      { path: 'emojis', name: RouteNames.ADMIN_EMOJIS, component: lazyRoute(() => import('@/domains/admin/presentation/pages/EmojiAdminPage.vue'), pageHeading), meta: { adminPermission: 'emojis' } },
      { path: 'invites', name: RouteNames.ADMIN_INVITES, component: lazyRoute(() => import('@/domains/admin/presentation/pages/InvitesAdminPage.vue'), pageHeading), meta: { adminPermission: 'invites' } },
      { path: 'roles', name: RouteNames.ADMIN_ROLES, component: lazyRoute(() => import('@/domains/admin/presentation/pages/RolesAdminPage.vue'), pageHeading), meta: { adminPermission: 'roles' } },
      { path: 'instances', name: RouteNames.ADMIN_INSTANCES, component: lazyRoute(() => import('@/domains/admin/presentation/pages/InstancesAdminPage.vue'), pageHeading), meta: { adminPermission: 'federation' } },
      { path: 'jobs', name: RouteNames.ADMIN_JOBS, component: lazyRoute(() => import('@/domains/admin/presentation/pages/JobsAdminPage.vue'), pageHeading), meta: { adminPermission: 'system' } },
      { path: 'files', name: RouteNames.ADMIN_FILES, component: lazyRoute(() => import('@/domains/admin/presentation/pages/FilesAdminPage.vue'), pageHeading), meta: { adminPermission: 'media' } },
      { path: 'announcements', name: RouteNames.ADMIN_ANNOUNCEMENTS, component: lazyRoute(() => import('@/domains/admin/presentation/pages/AnnouncementsAdminPage.vue'), pageHeading), meta: { adminPermission: 'announcements' } },
      { path: 'audit-logs', name: RouteNames.ADMIN_AUDIT, component: lazyRoute(() => import('@/domains/admin/presentation/pages/AuditLogsAdminPage.vue'), pageHeading), meta: { adminPermission: 'audit' } },
      { path: 'webhooks', name: RouteNames.ADMIN_WEBHOOKS, component: lazyRoute(() => import('@/domains/admin/presentation/pages/WebhooksAdminPage.vue'), pageHeading), meta: { adminPermission: 'webhooks' } },
      { path: 'settings', name: RouteNames.ADMIN_SETTINGS, redirect: '/admin/settings/general', meta: { adminPermission: 'settings' } },
      { path: 'moderation', name: RouteNames.ADMIN_MODERATION, component: lazyRoute(() => import('@/domains/admin/presentation/pages/ModerationSettingsAdminPage.vue'), pageHeading), meta: { adminPermission: 'settings' } },
      { path: 'rules', name: RouteNames.ADMIN_RULES, component: lazyRoute(() => import('@/domains/admin/presentation/pages/SettingsAdminPage.vue'), pageHeading), props: { mode: 'rules' }, meta: { adminPermission: 'rules' } },
      { path: 'decorations', name: RouteNames.ADMIN_DECORATIONS, component: BrandAssetsAdminPageRoute, props: { kind: 'avatar_decorations' }, meta: { adminPermission: 'branding' } },
      { path: 'advertisements', name: RouteNames.ADMIN_ADVERTISEMENTS, component: BrandAssetsAdminPageRoute, props: { kind: 'advertisements' }, meta: { adminPermission: 'branding' } },
      { path: 'settings/general', name: 'admin-settings-general', component: SettingsCenterPageRoute, props: { section: 'general' }, meta: { adminPermission: 'settings' } },
      { path: 'settings/branding', name: 'admin-settings-branding', component: SettingsCenterPageRoute, props: { section: 'branding' }, meta: { adminPermission: 'branding' } },
      { path: 'settings/moderation', name: 'admin-settings-moderation', component: SettingsCenterPageRoute, props: { section: 'moderation' }, meta: { adminPermission: 'settings' } },
      { path: 'settings/email', name: 'admin-settings-email', component: SettingsCenterPageRoute, props: { section: 'email' }, meta: { adminPermission: 'settings' } },
      { path: 'settings/storage', name: 'admin-settings-storage', component: SettingsCenterPageRoute, props: { section: 'storage' }, meta: { adminPermission: 'settings' } },
      { path: 'settings/security', name: 'admin-settings-security', component: SettingsCenterPageRoute, props: { section: 'security' }, meta: { adminPermission: 'settings' } },
      { path: 'settings/external', name: 'admin-settings-external', component: SettingsCenterPageRoute, props: { section: 'external' }, meta: { adminPermission: 'settings' } },
      { path: 'settings/performance', name: 'admin-settings-performance', component: SettingsCenterPageRoute, props: { section: 'performance' }, meta: { adminPermission: 'settings' } },
      { path: 'settings/database', name: 'admin-settings-database', component: SettingsCenterPageRoute, props: { section: 'database' }, meta: { adminPermission: 'system' } },
      { path: 'migrations', name: RouteNames.ADMIN_MIGRATIONS, component: DataTransferPageRoute, props: { scope: 'site' }, meta: { adminRootOnly: true } },
    ],
  },
  // Mastodon web paths.
  { path: '/public', redirect: { name: RouteNames.EXPLORE, query: { tab: 'federated' } } },
  { path: '/public/local', redirect: { name: RouteNames.EXPLORE, query: { tab: 'local' } } },
  { path: '/federated', redirect: { name: RouteNames.EXPLORE, query: { tab: 'federated' } } },
  { path: '/timeline/public', redirect: { name: RouteNames.EXPLORE, query: { tab: 'federated' } } },
  {
    path: '/:pathMatch(.*)*',
    name: 'not-found',
    component: lazyRoute(() => import('@/shared/components/common/NotFoundPage.vue'), pageHeading),
  },
]

// Export route titles for programmatic use (e.g., document.title updates)
export const RouteTitles: Partial<Record<RouteName, { title: string }>> = {
  [RouteNames.LANDING]: { title: '欢迎' },
  [RouteNames.LOGIN]: { title: '登录' },
  [RouteNames.REGISTER]: { title: '注册' },
  [RouteNames.HOME]: { title: '首页' },
  [RouteNames.EXPLORE]: { title: '探索' },
  [RouteNames.TOPICS]: { title: '发现话题' },
  [RouteNames.NOTIFICATIONS]: { title: '通知' },
  [RouteNames.MESSAGES]: { title: '私信' },
  [RouteNames.CHAT_CONVERSATION]: { title: '会话' },
  [RouteNames.GROUPS]: { title: '群组' },
  [RouteNames.GROUP]: { title: '群组' },
  [RouteNames.BOOKMARKS]: { title: '收藏' },
  [RouteNames.SEARCH]: { title: '搜索' },
  [RouteNames.PROFILE]: { title: '个人主页' },
  [RouteNames.POST_DETAIL]: { title: '帖子' },
  [RouteNames.TAG]: { title: '话题' },
  [RouteNames.FOLLOWERS]: { title: '关注者' },
  [RouteNames.FOLLOWING]: { title: '正在关注' },
  [RouteNames.LISTS]: { title: '列表' },
  [RouteNames.LIST_TIMELINE]: { title: '列表' },
  [RouteNames.SETTINGS]: { title: '设置' },
  [RouteNames.ADMIN_OVERVIEW]: { title: '管理后台' },
  [RouteNames.ADMIN_ACCOUNTS]: { title: '用户管理' },
  [RouteNames.ADMIN_REPORTS]: { title: '举报与内容' },
  [RouteNames.ADMIN_FEDERATION]: { title: '联邦中继' },
  [RouteNames.ADMIN_SYSTEM]: { title: 'Matrix 服务' },
  [RouteNames.ADMIN_SETTINGS]: { title: '站点设置' },
  [RouteNames.ADMIN_MIGRATIONS]: { title: '整站迁移' },
  [RouteNames.PERSONAL_DATA]: { title: '个人数据导入与导出' },
  [RouteNames.ADMIN_EMOJIS]: { title: '自定义表情管理' },
  [RouteNames.ABOUT]: { title: '关于' },
  [RouteNames.ANNOUNCEMENTS]: { title: '社区公告' },
}

export function pageHeading(name: unknown): string {
  const matched = routes.find(r => r.path === '/admin')?.children?.find(r => r.name === name)
  const adminTitle = matched ? adminNavigation.flatMap(g => g.items).find(i => i.path === '/admin' + (matched.path ? '/' + matched.path : ''))?.title : undefined
  const t = typeof name === 'string' ? RouteTitles[name as RouteName]?.title ?? adminTitle : undefined
  return t || 'Community'
}

/** Document title for a route: "<page> - Community", or just the app name. */
export function pageTitle(name: unknown, appName = 'Community'): string {
  const title = pageHeading(name)
  return title === 'Community' ? appName : `${title} - ${appName}`
}
