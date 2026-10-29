import { nextTick, onScopeDispose, useTemplateRef, watch } from 'vue'
import { sessionEpoch } from '@/shared/api/sessionScope'

export interface TimelineHeadTarget {
  revealHead: (headerHeight: number, focus: boolean) => Promise<void>
}

/** Commit first, then navigate to the new head. A failed or obsolete read never scrolls. */
export function useTimelineReveal(
  load: () => boolean | Promise<boolean>,
  scope: () => string,
  error: () => string | null,
) {
  const header = useTemplateRef<HTMLElement>('timelineHeader')
  const feed = useTemplateRef<TimelineHeadTarget>('timelineView')
  let version = 0
  watch([scope, sessionEpoch], () => { version++ }, { flush: 'sync' })
  onScopeDispose(() => { version++ })

  function reveal() {
    const mine = version
    const trigger = document.activeElement
    const finish = async (result: boolean) => {
      if (mine !== version) return
      if (!result) {
        if (error()) throw new Error(error()!)
        return
      }
      await nextTick()
      if (mine !== version) return
      const focus = trigger instanceof HTMLElement && trigger.dataset.testid === 'new-posts-pill'
        && (document.activeElement === trigger || document.activeElement === document.body)
      await feed.value?.revealHead?.(header.value?.getBoundingClientRect().height ?? 0, focus)
    }
    const result = load()
    if (typeof result !== 'boolean') return result.then(finish)
    if (!result && error()) throw new Error(error()!)
    // Applying a ready head is synchronous. Its DOM positioning needs a tick,
    // but should not flash the network-loading state.
    void finish(result)
  }
  function scrollToHead() {
    return feed.value?.revealHead?.(header.value?.getBoundingClientRect().height ?? 0, false)
  }
  return { reveal, scrollToHead }
}
