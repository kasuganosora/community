import { ref } from 'vue'

/**
 * App-wide presentation state for chrome that lives outside any one page:
 * the compose sheet and the account sheet. UI state only — no data.
 */
const composeOpen = ref(false)
const accountOpen = ref(false)

export function useUiChrome() {
  return {
    composeOpen,
    accountOpen,
    openCompose: () => { composeOpen.value = true },
    closeCompose: () => { composeOpen.value = false },
    openAccount: () => { accountOpen.value = true },
    closeAccount: () => { accountOpen.value = false },
  }
}
