<script setup lang="ts">
import { provide, onBeforeUnmount } from 'vue'
import { timelineRowKey } from '@/shared/composables/useTimelineRow'
const props = defineProps<{ id: string; flags: Record<string, boolean>; requestPin: (id: string, active: boolean) => boolean }>()
const reasons = new Set<symbol>()
const focus = Symbol('focus')
function pin(reason: symbol, active: boolean) {
  if (active && !props.requestPin(props.id, true)) return false
  if (active) reasons.add(reason)
  else reasons.delete(reason)
  props.requestPin(props.id, reasons.size > 0)
  return true
}
provide(timelineRowKey, { flags: props.flags, pin })
function focusOut(event: FocusEvent) {
  if (!(event.currentTarget as HTMLElement).contains(event.relatedTarget as Node | null)) pin(focus, false)
}
onBeforeUnmount(() => props.requestPin(props.id, false))
</script>
<template>
  <div class="timeline-row" :data-timeline-id="id" @focusin="pin(focus, true)" @focusout="focusOut"><slot /></div>
</template>
