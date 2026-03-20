import { createRouter, createWebHistory } from 'vue-router'
import { routes } from './routes'
import { AUTH_TOKEN_KEY } from '@/shared/api/client'

// Extend RouteMeta interface
declare module 'vue-router' {
  interface RouteMeta {
    guestOnly?: boolean
    requiresAuth?: boolean
  }
}

const router = createRouter({
  history: createWebHistory(),
  routes,
})

router.beforeEach((to) => {
  const token = localStorage.getItem(AUTH_TOKEN_KEY)

  // Redirect authenticated users away from guest-only pages to home
  if (to.meta.guestOnly && token) {
    return { name: 'home' }
  }

  // Redirect unauthenticated users to landing page
  if (to.meta.requiresAuth && !token) {
    return { name: 'landing' }
  }
})

export default router
