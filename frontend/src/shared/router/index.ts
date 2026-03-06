import { createRouter, createWebHistory } from 'vue-router'
import { routes } from './routes'

const router = createRouter({
  history: createWebHistory(),
  routes,
})

router.beforeEach((to) => {
  const token = localStorage.getItem('auth_token')

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
