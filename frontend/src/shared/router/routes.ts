import type { RouteRecordRaw } from 'vue-router'

// Define route names as constants for better maintainability
export const RouteNames = {
  LANDING: 'landing',
  LOGIN: 'login',
  REGISTER: 'register',
  HOME: 'home',
  EXPLORE: 'explore',
  NOTIFICATIONS: 'notifications',
  MESSAGES: 'messages',
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
  ADMIN_EMOJIS: 'admin-emojis',
  ABOUT: 'about',
} as const

/** Union of all route name string literals */
export type RouteName = (typeof RouteNames)[keyof typeof RouteNames]

// Route groups for better code splitting
const AuthRoutes: RouteRecordRaw[] = [
  {
    path: '/',
    name: RouteNames.LANDING,
    component: () => import('@/domains/auth/presentation/pages/LandingPage.vue'),
    meta: { guestOnly: true },
  },
  {
    path: '/login',
    name: RouteNames.LOGIN,
    component: () => import('@/domains/auth/presentation/pages/LoginPage.vue'),
    meta: { guestOnly: true },
  },
  {
    path: '/register',
    name: RouteNames.REGISTER,
    component: () => import('@/domains/auth/presentation/pages/RegisterPage.vue'),
    meta: { guestOnly: true },
  },
]

// Public (no auth, no guest-only redirect): instance info, rules and terms.
const PublicRoutes: RouteRecordRaw[] = [
  {
    path: '/about',
    name: RouteNames.ABOUT,
    component: () => import('@/domains/instance/presentation/pages/AboutPage.vue'),
  },
]

const MainRoutes: RouteRecordRaw[] = [
  {
    path: 'admin/emojis',
    name: RouteNames.ADMIN_EMOJIS,
    component: () => import('@/domains/admin/presentation/pages/EmojiAdminPage.vue'),
    meta: { requiresAdmin: true },
  },
  {
    path: '',
    name: RouteNames.HOME,
    component: () => import('@/domains/timeline/presentation/pages/HomePage.vue'),
  },
  {
    path: 'explore',
    name: RouteNames.EXPLORE,
    component: () => import('@/domains/timeline/presentation/pages/ExplorePage.vue'),
  },
  {
    path: 'notifications',
    name: RouteNames.NOTIFICATIONS,
    component: () => import('@/domains/notification/presentation/pages/NotificationsPage.vue'),
  },
  {
    path: 'messages',
    name: RouteNames.MESSAGES,
    component: () => import('@/domains/message/presentation/pages/MessagesPage.vue'),
  },
  {
    path: 'bookmarks',
    name: RouteNames.BOOKMARKS,
    component: () => import('@/domains/bookmark/presentation/pages/BookmarksPage.vue'),
  },
  {
    path: 'search',
    name: RouteNames.SEARCH,
    component: () => import('@/domains/trending/presentation/pages/SearchResultsPage.vue'),
  },
  {
    path: 'settings',
    name: RouteNames.SETTINGS,
    component: () => import('@/domains/settings/presentation/pages/SettingsPage.vue'),
  },
  {
    path: '@:username',
    name: RouteNames.PROFILE,
    component: () => import('@/domains/user/presentation/pages/ProfilePage.vue'),
    props: true,
  },
  {
    path: '@:username/followers',
    name: RouteNames.FOLLOWERS,
    component: () => import('@/domains/user/presentation/pages/FollowListPage.vue'),
    props: { kind: 'followers' },
  },
  {
    path: '@:username/following',
    name: RouteNames.FOLLOWING,
    component: () => import('@/domains/user/presentation/pages/FollowListPage.vue'),
    props: { kind: 'following' },
  },
  {
    path: 'tags/:tag',
    name: RouteNames.TAG,
    component: () => import('@/domains/timeline/presentation/pages/TagTimelinePage.vue'),
  },
  {
    path: 'lists',
    name: RouteNames.LISTS,
    component: () => import('@/domains/list/presentation/pages/ListsPage.vue'),
  },
  {
    path: 'lists/:id',
    name: RouteNames.LIST_TIMELINE,
    component: () => import('@/domains/list/presentation/pages/ListTimelinePage.vue'),
  },
  {
    path: '@:username/status/:statusId',
    alias: '@:username/:statusId(\\d+)',
    name: RouteNames.POST_DETAIL,
    component: () => import('@/domains/post/presentation/pages/PostDetailPage.vue'),
    props: true,
  },
]

export const routes: RouteRecordRaw[] = [
  ...AuthRoutes,
  ...PublicRoutes,
  {
    path: '/',
    component: () => import('@/shared/components/layout/AppLayout.vue'),
    meta: { requiresAuth: true },
    children: MainRoutes,
  },
  {
    path: '/:pathMatch(.*)*',
    name: 'not-found',
    component: () => import('@/shared/components/common/NotFoundPage.vue'),
  },
]

// Export route titles for programmatic use (e.g., document.title updates)
export const RouteTitles: Partial<Record<RouteName, { title: string }>> = {
  [RouteNames.LANDING]: { title: '欢迎' },
  [RouteNames.LOGIN]: { title: '登录' },
  [RouteNames.REGISTER]: { title: '注册' },
  [RouteNames.HOME]: { title: '首页' },
  [RouteNames.EXPLORE]: { title: '探索' },
  [RouteNames.NOTIFICATIONS]: { title: '通知' },
  [RouteNames.MESSAGES]: { title: '私信' },
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
  [RouteNames.ADMIN_EMOJIS]: { title: '自定义表情管理' },
  [RouteNames.ABOUT]: { title: '关于' },
}

/** Document title for a route: "<page> - Community", or just the app name. */
export function pageTitle(name: unknown, appName = 'Community'): string {
  const t = typeof name === 'string' ? RouteTitles[name as RouteName]?.title : undefined
  return t ? `${t} - ${appName}` : appName
}
