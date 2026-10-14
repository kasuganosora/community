import { onBeforeUnmount, watch } from 'vue'
import { sessionEpoch } from '@/shared/api/sessionScope'

/** Keep pending settings requests and multi-step mutations within their original account. */
export function useSettingsScope(reset: () => void) {
  let active = true
  watch(sessionEpoch, reset, { flush: 'sync' })
  onBeforeUnmount(() => { active = false })
  return () => {
    const epoch = sessionEpoch.value
    return () => active && epoch === sessionEpoch.value
  }
}
