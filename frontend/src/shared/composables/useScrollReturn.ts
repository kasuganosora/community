import { onBeforeUnmount, onMounted, useTemplateRef, watch } from 'vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { prefersReducedMotion } from '@/shared/motion/spring'
import { isInteractiveClick } from '@/shared/utils/dom'

export interface ScrollAnchorPoint {
  id?: string
  top: number
  scrollY: number
}

export interface ScrollReturnAdapter {
  capture: () => ScrollAnchorPoint
  restore: (point: ScrollAnchorPoint) => Promise<void> | void
  scrollToTop?: () => Promise<void> | void
}

export interface TimelineScrollTarget {
  captureAnchor?: () => ScrollAnchorPoint
  restoreAnchor?: (point: ScrollAnchorPoint) => Promise<void> | void
  scrollToTop?: () => Promise<void> | void
}

export const SCROLL_TOP_THRESHOLD = 64
const MANUAL_SCROLL_EXPIRE = 200
const MAX_SAVED_SCOPES = 8

interface SavedReadPosition {
  point: ScrollAnchorPoint
  reachedTop: boolean
}

const savedPositions = new Map<string, SavedReadPosition>()
let activeBinding: { scope: () => string; adapter?: ScrollReturnAdapter } | null = null
let programmaticUntil = 0

function now(): number {
  return typeof performance !== 'undefined' ? performance.now() : Date.now()
}

function currentScope(): string {
  if (activeBinding) return activeBinding.scope()
  return typeof window !== 'undefined' ? window.location.pathname : 'default'
}

watch(sessionEpoch, () => {
  savedPositions.clear()
  programmaticUntil = 0
}, { flush: 'sync' })

export function clearScrollReturnMemory() {
  savedPositions.clear()
  programmaticUntil = 0
}

export function hasSavedScrollReturn(scope = currentScope()): boolean {
  return savedPositions.has(scope)
}

/**
 * Tweetbot-style "Tap to Top & Tap Again to Return to Last Read":
 * - When scrolled down (> 64px), saves the current reading anchor and scrolls to top.
 * - When already at top (<= 64px) with a saved anchor for this timeline, returns to it.
 */
export function triggerScrollTopOrReturn(): void {
  if (typeof window === 'undefined') return
  const scope = currentScope()
  const y = window.scrollY
  const adapter = activeBinding?.adapter
  const behavior: ScrollBehavior = prefersReducedMotion() ? 'instant' : 'smooth'

  if (y > SCROLL_TOP_THRESHOLD) {
    const point = adapter?.capture() ?? { top: 0, scrollY: y }
    savedPositions.delete(scope)
    savedPositions.set(scope, { point, reachedTop: false })
    while (savedPositions.size > MAX_SAVED_SCOPES) {
      const oldest = savedPositions.keys().next().value
      if (oldest !== undefined) savedPositions.delete(oldest)
    }
    programmaticUntil = now() + 900
    if (adapter?.scrollToTop) {
      void Promise.resolve(adapter.scrollToTop()).finally(() => {
        const entry = savedPositions.get(scope)
        if (entry && window.scrollY <= SCROLL_TOP_THRESHOLD) entry.reachedTop = true
      })
    } else {
      window.scrollTo({ top: 0, behavior })
      if (window.scrollY <= SCROLL_TOP_THRESHOLD) {
        const entry = savedPositions.get(scope)
        if (entry) entry.reachedTop = true
      }
    }
    return
  }

  const saved = savedPositions.get(scope)
  if (saved) {
    savedPositions.delete(scope)
    programmaticUntil = now() + 900
    if (adapter?.restore) {
      void adapter.restore(saved.point)
    } else {
      window.scrollTo({ top: Math.max(0, saved.point.scrollY), behavior })
    }
    return
  }

  if (y > 0) {
    programmaticUntil = now() + 500
    if (adapter?.scrollToTop) void adapter.scrollToTop()
    else window.scrollTo({ top: 0, behavior })
  }
}

/**
 * Registers a timeline page with the global Tap-to-Top / Return-to-Last-Read
 * coordinator used by page headers, TabBar, MobileTabBar, and LeftSidebar.
 */
export function useScrollReturn(scope: () => string, adapter?: ScrollReturnAdapter) {
  const binding = { scope, adapter }

  function onScroll() {
    const key = scope()
    const entry = savedPositions.get(key)
    if (!entry) return
    const y = window.scrollY
    if (y <= SCROLL_TOP_THRESHOLD) {
      entry.reachedTop = true
      return
    }
    // Once the user has landed at the top and manually scrolls down into the
    // feed again, expire the return-to-last-read bookmark so a later tap goes up.
    if (entry.reachedTop && y > MANUAL_SCROLL_EXPIRE && now() > programmaticUntil) {
      savedPositions.delete(key)
    }
  }

  function onHeaderTap(e: MouseEvent) {
    if (isInteractiveClick(e)) return
    triggerScrollTopOrReturn()
  }

  onMounted(() => {
    activeBinding = binding
    window.addEventListener('scroll', onScroll, { passive: true })
  })

  onBeforeUnmount(() => {
    if (activeBinding === binding) activeBinding = null
    window.removeEventListener('scroll', onScroll)
  })

  return {
    onHeaderTap,
    trigger: triggerScrollTopOrReturn,
  }
}

/**
 * Convenience wrapper for pages rendering `<TimelineFeed ref="timelineView" />`
 * so Tap-to-Top / Return-to-Last-Read restores the exact virtualized card anchor.
 */
export function useTimelineScrollReturn(
  scope: () => string,
  existingView?: { readonly value: TimelineScrollTarget | null },
) {
  const view = existingView ?? useTemplateRef<TimelineScrollTarget>('timelineView')
  return useScrollReturn(scope, {
    capture: () => view.value?.captureAnchor?.() ?? { top: 0, scrollY: window.scrollY },
    restore: (point) => {
      if (view.value?.restoreAnchor) return view.value.restoreAnchor(point)
      const behavior: ScrollBehavior = prefersReducedMotion() ? 'instant' : 'smooth'
      window.scrollTo({ top: Math.max(0, point.scrollY), behavior })
    },
    scrollToTop: () => {
      if (view.value?.scrollToTop) return view.value.scrollToTop()
      const behavior: ScrollBehavior = prefersReducedMotion() ? 'instant' : 'smooth'
      window.scrollTo({ top: 0, behavior })
    },
  })
}
