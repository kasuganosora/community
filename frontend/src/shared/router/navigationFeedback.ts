import { ref, type Ref } from 'vue'
import { isNavigationFailure, NavigationFailureType, type Router, type RouteLocationNormalized } from 'vue-router'

export const navigationPending = ref(false)

/** Permission checks still happen before navigation. Only a neutral progress
 * indicator is exposed while they run; cancelled older visits cannot hide it. */
export function trackNavigation(router: Router, pending: Ref<boolean> = navigationPending) {
  let target: RouteLocationNormalized | null = null
  const start = router.beforeEach(to => { target = to; pending.value = true })
  function finish(to: RouteLocationNormalized) {
    if (target !== to) return
    target = null
    pending.value = false
  }
  const complete = router.afterEach((to, _from, failure) => {
    // Re-selecting the current page cancels an outstanding navigation, but Vue
    // Router intentionally skips beforeEach for that duplicate request.
    if (isNavigationFailure(failure, NavigationFailureType.duplicated)) {
      target = null
      pending.value = false
    } else finish(to)
  })
  const error = router.onError((_error, to) => finish(to))
  return () => { start(); complete(); error(); target = null; pending.value = false }
}
