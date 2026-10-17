import { onBeforeUnmount, shallowRef, watch, type Ref } from 'vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { useTimelineRowPin } from './useTimelineRow'
import type { MastodonAccount } from '@/shared/types/mastodon'

export interface AccountHoverTarget { acct: string; account?: MastodonAccount }
export const accountHover = shallowRef<(AccountHoverTarget & { anchor: HTMLElement }) | null>(null)
let opening: ReturnType<typeof setTimeout> | undefined
let closing: ReturnType<typeof setTimeout> | undefined
let pendingAnchor: HTMLElement | null = null

export function keepAccountHover() { clearTimeout(closing) }
export function closeAccountHover() {
  clearTimeout(opening)
  clearTimeout(closing)
  pendingAnchor = null
  accountHover.value = null
}
export function leaveAccountHover() {
  clearTimeout(opening)
  pendingAnchor = null
  clearTimeout(closing)
  closing = setTimeout(closeAccountHover, 180)
}
export function showAccountHover(anchor: HTMLElement, target: AccountHoverTarget) {
  keepAccountHover()
  if (accountHover.value?.anchor === anchor || pendingAnchor === anchor) return
  clearTimeout(opening)
  pendingAnchor = anchor
  opening = setTimeout(() => {
    pendingAnchor = null
    if (anchor.isConnected) accountHover.value = { ...target, anchor }
  }, 300)
}
export function focusAccountHover(event: KeyboardEvent) {
  if (!accountHover.value?.anchor.contains(event.target as Node)) return
  if ((event.key === 'Tab' && !event.shiftKey) || event.key === 'ArrowDown') {
    const first = document.querySelector<HTMLElement>('#account-hover-card a, #account-hover-card button')
    if (first && accountHover.value) { event.preventDefault(); first.focus() }
  }
  if (event.key === 'Escape') closeAccountHover()
}
watch(sessionEpoch, closeAccountHover, { flush: 'sync' })

/** Keep virtualized rows mounted while their card is in use. */
export function useAccountHoverOwner(root: Ref<HTMLElement | undefined>) {
  const owns = () => !!root.value?.contains(accountHover.value?.anchor ?? null)
  useTimelineRowPin(owns, closeAccountHover)
  onBeforeUnmount(() => {
    if (owns() || root.value?.contains(pendingAnchor)) closeAccountHover()
  })
}
