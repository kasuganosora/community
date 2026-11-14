import { ref, watch } from 'vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import type { MastodonStatus } from '@/shared/types/mastodon'

/**
 * App-wide presentation state for chrome that lives outside any one page:
 * the compose sheet and the account sheet. UI state only — no data.
 */
const composeOpen = ref(false)
const accountOpen = ref(false)
const accountsManagerOpen = ref(false)
const quoteTarget = ref<MastodonStatus | null>(null)
const replyTarget = ref<MastodonStatus | null>(null)

function unwrapStatus(status: MastodonStatus): MastodonStatus {
  return status.reblog?.id ? status.reblog : status
}

watch(sessionEpoch, () => {
  composeOpen.value = false
  accountOpen.value = false
  accountsManagerOpen.value = false
  quoteTarget.value = null
  replyTarget.value = null
}, { flush: 'sync' })

export function useUiChrome() {
  return {
    composeOpen,
    accountOpen,
    accountsManagerOpen,
    quoteTarget,
    replyTarget,
    openCompose: () => {
      quoteTarget.value = null
      replyTarget.value = null
      composeOpen.value = true
    },
    openQuote: (status: MastodonStatus) => {
      replyTarget.value = null
      quoteTarget.value = unwrapStatus(status)
      composeOpen.value = true
    },
    openReply: (status: MastodonStatus) => {
      quoteTarget.value = null
      replyTarget.value = unwrapStatus(status)
      composeOpen.value = true
    },
    closeCompose: () => { composeOpen.value = false },
    openAccount: () => { accountOpen.value = true },
    closeAccount: () => { accountOpen.value = false },
    openAccountsManager: () => { accountOpen.value = false; accountsManagerOpen.value = true },
  }
}
