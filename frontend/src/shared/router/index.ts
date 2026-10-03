import { createRouter, createWebHistory } from 'vue-router'
import { routes, pageTitle } from './routes'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { tokenService } from '@/shared/api/client'

// Extend RouteMeta interface
declare module 'vue-router' {
  interface RouteMeta {
    guestOnly?: boolean
    requiresAuth?: boolean
    requiresAdmin?: boolean
    title?: string
  }
}

const router = createRouter({
  history: createWebHistory(),
  routes,
})

/**
 * Validate redirect URL to prevent open redirect attacks.
 * Only allow relative paths starting with a single '/'.
 */
function isSafeRedirect(path: string): boolean {
  // Reject protocol-relative URLs: "//evil.com" and backslash variants "/\evil.com"
  return path.startsWith('/') && !path.startsWith('//') && !path.startsWith('/\\')
}

router.beforeEach(async (to) => {
  const token = tokenService.get()

  // Redirect authenticated users away from guest-only pages to home
  if (to.meta.guestOnly && token) {
    return { name: 'home' }
  }

  // Redirect unauthenticated users to landing page, preserving redirect target
  if (to.meta.requiresAuth && !token) {
    const redirectPath = to.fullPath !== '/' && isSafeRedirect(to.fullPath) ? { redirect: to.fullPath } : undefined
    return { name: 'landing', query: redirectPath }
  }

  if (to.meta.requiresAdmin) {
    const auth = useAuthStore()
    // Recheck permissions on entry; the API independently enforces authorization.
    if (!await auth.fetchCurrentUser() || auth.currentUser?.is_admin !== true) {
      return { name: 'settings' }
    }
  }
})

// Keep the document title in step with the page (screen readers announce it,
// and browser history/tabs were all just "Community").
router.afterEach((to) => {
  document.title = pageTitle(to.name)
})

export default router
