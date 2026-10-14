import { adminEntry, canAccessAdmin } from '@/domains/admin/domain/navigation'
import { createRouter, createWebHistory } from 'vue-router'
import { routes, pageTitle } from './routes'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { tokenService } from '@/shared/api/client'
import { isServerAuthReturn, safeInternalPath } from '@/shared/utils/authReturn'
import { trackNavigation } from './navigationFeedback'
import { preloadRouteViews } from './lazyRoute'

// Extend RouteMeta interface
declare module 'vue-router' {
  interface RouteMeta {
    guestOnly?: boolean
    requiresAuth?: boolean
    requiresAdmin?: boolean
    adminPermission?: string
    adminRootOnly?: boolean
    title?: string
  }
}

const router = createRouter({
  history: createWebHistory(),
  routes,
  scrollBehavior(_to, _from, savedPosition) {
    if (!savedPosition) return { top: 0 }
    // Wait a frame so a returning timeline has its rows before the offset is applied.
    return new Promise(resolve => {
      requestAnimationFrame(() => resolve(savedPosition))
    })
  },
})

trackNavigation(router)

/**
 * Validate redirect URL to prevent open redirect attacks.
 * Only allow relative paths starting with a single '/'.
 */
function isSafeRedirect(path: string): boolean {
  return safeInternalPath(path) != null
}

router.beforeEach(async (to) => {
  const token = tokenService.get()

  // An already signed-in user opening an OAuth / MiAuth link must land on the
  // server consent page, not be bounced to the home timeline.
  if (to.meta.guestOnly && token) {
    const back = safeInternalPath(to.query.redirect)
    if (back && isServerAuthReturn(back)) {
      window.location.assign(back)
      return false
    }
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
    if (!await auth.fetchCurrentUser()) return { name: 'settings' }
    if (!canAccessAdmin(auth.currentUser, to.meta.adminPermission, to.meta.adminRootOnly)) {
      const entry = adminEntry(auth.currentUser)
      return entry && entry !== to.path ? { path: entry } : { name: 'settings' }
    }
  }
})

router.beforeResolve(preloadRouteViews)

// Keep the document title in step with the page (screen readers announce it,
// and browser history/tabs were all just "Community").
router.afterEach((to, _from, failure) => {
  if (failure) return
  document.title = pageTitle(to.name)
})

export default router
