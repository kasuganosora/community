import { ref } from 'vue'
import client from '@/shared/api/client'

export type VisitorUgcVisibility = 'all' | 'local' | 'none'

const policy = ref<VisitorUgcVisibility>('all')
let loaded: Promise<void> | undefined

/**
 * Misskey-style ugcVisibilityForVisitor: what logged-out visitors may see
 * ("all", only local content, or nothing). Loaded once per page load.
 */
export function useVisitorPolicy() {
  if (!loaded) {
    loaded = client.get<{ ugc_visibility_for_visitor?: VisitorUgcVisibility }>('/api/v1/instance/visitor_policy')
      .then(r => { const v = r.data?.ugc_visibility_for_visitor; if (v === 'local' || v === 'none') policy.value = v })
      .catch(() => { loaded = undefined })
  }
  return { policy, ready: loaded }
}
