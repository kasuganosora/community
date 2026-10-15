import { ref } from 'vue'
import type { MastodonStatus } from '@/shared/types/mastodon'

/**
 * App-wide presentation state for chrome that lives outside any one page:
 * the compose sheet and the account sheet. UI state only — no data.
 */
const composeOpen = ref(false)
const accountOpen = ref(false)
const accountsManagerOpen = ref(false)
const quoteTarget = ref<MastodonStatus | null>(null)

function quotedStatus(status: MastodonStatus): MastodonStatus {
  return status.reblog?.id ? status.reblog : status
}

export function useUiChrome() {
  return {
    composeOpen,
    accountOpen,
    accountsManagerOpen,
    quoteTarget,
    openCompose: () => { quoteTarget.value = null; composeOpen.value = true },
    openQuote: (status: MastodonStatus) => { quoteTarget.value = quotedStatus(status); composeOpen.value = true },
    closeCompose: () => { composeOpen.value = false },
    openAccount: () => { accountOpen.value = true },
    closeAccount: () => { accountOpen.value = false },
    openAccountsManager: () => { accountOpen.value = false; accountsManagerOpen.value = true },
  }
}
