import { ref, watch } from 'vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { discoveryApi, type DiscoveryPreference } from '../../infrastructure/discoveryApi'

// Never persist user-scoped discovery data in URL-only or localStorage caches.
export const discoveryPreference = ref<DiscoveryPreference | null>(null)
export const discoveryInvalidation = ref(0)
let pending: Promise<DiscoveryPreference | null> | undefined
let revision = 0
watch(sessionEpoch, () => {
  revision++
  pending = undefined
  discoveryPreference.value = null
  discoveryInvalidation.value++
}, { flush: 'sync' })

export function acceptDiscoveryPreference(value: DiscoveryPreference) {
  if (!value || !Number.isFinite(value.revision)) return
  if (discoveryPreference.value && value.revision < discoveryPreference.value.revision) return
  discoveryPreference.value = { ...discoveryPreference.value, ...value }
}

export function invalidateDiscovery(value?: DiscoveryPreference) {
  revision++
  pending = undefined
  if (value) acceptDiscoveryPreference(value)
  discoveryInvalidation.value++
}

export function loadDiscoveryPreference(force = false): Promise<DiscoveryPreference | null> {
  if (pending && !force) return pending
  const mine = ++revision, epoch = sessionEpoch.value
  const request = discoveryApi.preferences().then(value => {
    if (mine !== revision || epoch !== sessionEpoch.value) return null
    acceptDiscoveryPreference(value)
    return value
  }).finally(() => { if (mine === revision) pending = undefined })
  pending = request
  return request
}
