import { inject, onBeforeUnmount, ref, toRef, watch, type InjectionKey, type Ref } from 'vue'

/** Optional timeline ownership: detail pages keep their normal component state. */
export interface TimelineRowContext {
  flags: Record<string, boolean>
  pin: (reason: symbol, active: boolean) => boolean
}
export const timelineRowKey: InjectionKey<TimelineRowContext> = Symbol('timeline-row')

export function useTimelineRowFlag(name: string): Ref<boolean> {
  const row = inject(timelineRowKey, null)
  return row ? toRef(row.flags, name, false) : ref(false)
}

export function useTimelineRowPin(active: () => boolean, rejected?: () => void) {
  const row = inject(timelineRowKey, null)
  if (!row) return
  const reason = Symbol('interaction')
  watch(active, value => {
    if (!row.pin(reason, value) && value) rejected?.()
  }, { immediate: true, flush: 'sync' })
  onBeforeUnmount(() => row.pin(reason, false))
}
