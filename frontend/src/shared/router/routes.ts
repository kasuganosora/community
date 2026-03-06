import type { RouteRecordRaw } from 'vue-router'

export const routes: RouteRecordRaw[] = [
  {
    path: '/',
    name: 'landing',
    component: () => import('@/domains/auth/presentation/pages/LandingPage.vue'),
    meta: { guestOnly: true },
  },
  {
    path: '/login',
    name: 'login',
    component: () => import('@/domains/auth/presentation/pages/LoginPage.vue'),
    meta: { guestOnly: true },
  },
  {
    path: '/register',
    name: 'register',
    component: () => import('@/domains/auth/presentation/pages/RegisterPage.vue'),
    meta: { guestOnly: true },
  },
  {
    path: '/home',
    component: () => import('@/shared/components/layout/AppLayout.vue'),
    meta: { requiresAuth: true },
    children: [
      {
        path: '',
        name: 'home',
        component: () => import('@/domains/timeline/presentation/pages/HomePage.vue'),
      },
      {
        path: '/explore',
        name: 'explore',
        component: () => import('@/domains/timeline/presentation/pages/ExplorePage.vue'),
      },
      {
        path: '/notifications',
        name: 'notifications',
        component: () => import('@/domains/notification/presentation/pages/NotificationsPage.vue'),
      },
      {
        path: '/messages',
        name: 'messages',
        component: () => import('@/domains/message/presentation/pages/MessagesPage.vue'),
      },
      {
        path: '/bookmarks',
        name: 'bookmarks',
        component: () => import('@/domains/bookmark/presentation/pages/BookmarksPage.vue'),
      },
      {
        path: '/search',
        name: 'search',
        component: () => import('@/domains/trending/presentation/pages/SearchResultsPage.vue'),
      },
      {
        path: '/@:username',
        name: 'profile',
        component: () => import('@/domains/user/presentation/pages/ProfilePage.vue'),
        props: true,
      },
      {
        path: '/@:username/status/:statusId',
        name: 'post-detail',
        component: () => import('@/domains/post/presentation/pages/PostDetailPage.vue'),
        props: true,
      },
    ],
  },
]
