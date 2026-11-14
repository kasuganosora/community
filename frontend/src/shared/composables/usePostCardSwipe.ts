import { onBeforeUnmount, onMounted, readonly, ref, watch, type Ref } from 'vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { createSpring, prefersReducedMotion, rubberband, SPRING_DEFAULT } from '@/shared/motion/spring'

export type PostSwipeAction = 'like' | 'reply' | 'detail'

export const SWIPE_HYSTERESIS = 12
export const RIGHT_LIKE_THRESHOLD = 44
export const RIGHT_REPLY_THRESHOLD = 92
export const LEFT_DETAIL_THRESHOLD = 52

function shouldIgnoreSwipeTarget(target: EventTarget | null): boolean {
  if (!(target instanceof Element)) return false
  if (target.closest('input, textarea, select, video, audio, [contenteditable="true"], [data-no-swipe]')) return true
  let node: Element | null = target
  while (node && !node.classList.contains('post-card')) {
    if (node instanceof HTMLElement && node.scrollWidth > node.clientWidth + 2) {
      const ox = getComputedStyle(node).overflowX
      if (ox === 'auto' || ox === 'scroll') return true
    }
    node = node.parentElement
  }
  return false
}

function mapSwipeOffset(rawDx: number, allowRight: boolean): number {
  if (rawDx > 0) {
    if (!allowRight) return rubberband(rawDx, 48, 0.25)
    if (rawDx <= 72) return rawDx * 0.85
    return 61.2 + rubberband(rawDx - 72, 140, 0.6)
  }
  const abs = -rawDx
  const mapped = abs <= 64 ? abs * 0.85 : 54.4 + rubberband(abs - 64, 120, 0.5)
  return -mapped
}

function actionForOffset(offset: number, allowRight: boolean): PostSwipeAction | null {
  if (offset > 0) {
    if (!allowRight) return null
    if (offset >= RIGHT_REPLY_THRESHOLD) return 'reply'
    if (offset >= RIGHT_LIKE_THRESHOLD) return 'like'
    return null
  }
  if (offset <= -LEFT_DETAIL_THRESHOLD) return 'detail'
  return null
}

/**
 * Tweetbot-style horizontal swipe gestures on a timeline row:
 * - Short swipe right: toggle Favourite (Like)
 * - Long swipe right: open Quick Reply sheet
 * - Swipe left: open Post Detail / Conversation
 */
export function usePostCardSwipe(
  cardEl: Ref<HTMLElement | undefined>,
  options: {
    enabled: () => boolean
    allowRightActions: () => boolean
    onLike: () => void
    onReply: () => void
    onDetail: () => void
  },
) {
  const swipeOffset = ref(0)
  const swipeAction = ref<PostSwipeAction | null>(null)
  const swiping = ref(false)

  const spring = createSpring(0, (v) => {
    swipeOffset.value = Math.abs(v) > 0.5 ? Math.round(v * 10) / 10 : 0
  })

  let state: 'idle' | 'pending' | 'swiping' = 'idle'
  let startX = 0
  let startY = 0
  let swipeOriginX = 0
  let suppressClick = false
  let boundEl: HTMLElement | undefined

  function resetSwipe() {
    state = 'idle'
    swiping.value = false
    swipeAction.value = null
    suppressClick = false
    spring.set(0)
  }

  watch(sessionEpoch, resetSwipe, { flush: 'sync' })

  function snapBack() {
    if (prefersReducedMotion()) spring.set(0)
    else void spring.to(0, SPRING_DEFAULT)
  }

  function onTouchStart(e: TouchEvent) {
    if (!options.enabled() || e.touches.length !== 1) return
    if (document.documentElement.classList.contains('has-sheet')) return
    if (shouldIgnoreSwipeTarget(e.target)) return
    const touch = e.touches[0]!
    startX = touch.clientX
    startY = touch.clientY
    state = 'pending'
  }

  function onTouchMove(e: TouchEvent) {
    if (state === 'idle') return
    if (!options.enabled() || e.touches.length !== 1) {
      cancelSwipe()
      return
    }
    const touch = e.touches[0]!
    const dx = touch.clientX - startX
    const dy = touch.clientY - startY

    if (state === 'pending') {
      if (Math.hypot(dx, dy) < SWIPE_HYSTERESIS) return
      // Require decisive horizontal angle (< ~33 deg) so diagonal thumb scrolls stay native vertical scrolls.
      if (Math.abs(dx) <= Math.abs(dy) * 1.5) {
        state = 'idle'
        return
      }
      state = 'swiping'
      swiping.value = true
      suppressClick = true
      swipeOriginX = touch.clientX
      spring.stop()
    }

    if (e.cancelable) e.preventDefault()
    const rawDx = touch.clientX - swipeOriginX
    const allowRight = options.allowRightActions()
    const offset = mapSwipeOffset(rawDx, allowRight)
    spring.set(offset)

    const nextAction = actionForOffset(offset, allowRight)
    if (nextAction !== swipeAction.value) {
      swipeAction.value = nextAction
      if (nextAction) navigator.vibrate?.(nextAction === 'reply' ? 10 : 8)
    }
  }

  function cancelSwipe() {
    const wasSwiping = state === 'swiping'
    state = 'idle'
    swiping.value = false
    swipeAction.value = null
    if (wasSwiping) {
      requestAnimationFrame(() => { suppressClick = false })
      snapBack()
    }
  }

  function onTouchEnd() {
    if (state !== 'swiping') {
      state = 'idle'
      return
    }
    state = 'idle'
    swiping.value = false
    const committed = swipeAction.value
    swipeAction.value = null
    requestAnimationFrame(() => { suppressClick = false })
    snapBack()

    if (committed === 'like') options.onLike()
    else if (committed === 'reply') options.onReply()
    else if (committed === 'detail') options.onDetail()
  }

  function consumeClickSuppression(e: MouseEvent): boolean {
    if (!suppressClick) return false
    suppressClick = false
    e.preventDefault()
    e.stopPropagation()
    return true
  }

  function bind(el: HTMLElement | undefined) {
    if (boundEl === el) return
    if (boundEl) {
      boundEl.removeEventListener('touchstart', onTouchStart)
      boundEl.removeEventListener('touchmove', onTouchMove)
      boundEl.removeEventListener('touchend', onTouchEnd)
      boundEl.removeEventListener('touchcancel', cancelSwipe)
    }
    boundEl = el
    if (el) {
      el.addEventListener('touchstart', onTouchStart, { passive: true })
      el.addEventListener('touchmove', onTouchMove, { passive: false })
      el.addEventListener('touchend', onTouchEnd, { passive: true })
      el.addEventListener('touchcancel', cancelSwipe, { passive: true })
    }
  }

  watch(cardEl, el => bind(el))
  onMounted(() => bind(cardEl.value))
  onBeforeUnmount(() => {
    bind(undefined)
    spring.stop()
  })

  return {
    swipeOffset: readonly(swipeOffset),
    swipeAction: readonly(swipeAction),
    swiping: readonly(swiping),
    consumeClickSuppression,
  }
}
