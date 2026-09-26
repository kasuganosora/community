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
  SETTINGS: 'settings',
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

const MainRoutes: RouteRecordRaw[] = [
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
    path: '@:username/status/:statusId',
    name: RouteNames.POST_DETAIL,
    component: () => import('@/domains/post/presentation/pages/PostDetailPage.vue'),
    props: true,
  },
]

export const routes: RouteRecordRaw[] = [
  ...AuthRoutes,
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
  [RouteNames.LANDING]: { title: 'Welcome' },
  [RouteNames.LOGIN]: { title: 'Sign In' },
  [RouteNames.REGISTER]: { title: 'Create Account' },
  [RouteNames.HOME]: { title: 'Home' },
  [RouteNames.EXPLORE]: { title: 'Explore' },
  [RouteNames.NOTIFICATIONS]: { title: 'Notifications' },
  [RouteNames.MESSAGES]: { title: 'Messages' },
  [RouteNames.BOOKMARKS]: { title: 'Bookmarks' },
  [RouteNames.SEARCH]: { title: 'Search' },
  [RouteNames.PROFILE]: { title: 'Profile' },
  [RouteNames.POST_DETAIL]: { title: 'Post' },
  [RouteNames.SETTINGS]: { title: 'Settings' },
}
