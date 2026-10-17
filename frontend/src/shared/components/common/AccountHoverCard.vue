<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { accountHover, closeAccountHover, keepAccountHover, leaveAccountHover } from '@/shared/composables/useAccountHover'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { userApi } from '@/domains/user/infrastructure/userApi'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { followButtonLabel, followStateOf } from '@/domains/user/domain/relationship'
import { displayAccount } from '@/shared/utils/accountDisplay'
import { formatCount } from '@/shared/utils/format'
import type { MastodonAccount, MastodonRelationship } from '@/shared/types/mastodon'
import UserAvatar from './UserAvatar.vue'
import EmojiText from './EmojiText.vue'
import RichText from './RichText.vue'

const auth = useAuthStore()
const route = useRoute()
const card = ref<HTMLElement>()
const account = ref<MastodonAccount>()
const relationship = ref<MastodonRelationship>()
const loading = ref(false)
const failed = ref(false)
const followFailed = ref(false)
const busy = ref(false)
const headerFailed = ref(false)
const position = ref({ left: '8px', top: '8px', visibility: 'hidden' as 'hidden' | 'visible' })
const profilePath = computed(() => `/@${account.value?.acct || accountHover.value?.acct || ''}`)
const state = computed(() => followStateOf(relationship.value))
const canFollow = computed(() => auth.isAuthenticated && account.value?.id !== auth.currentUser?.id && state.value !== 'unknown' && !relationship.value?.blocking && !relationship.value?.blocked_by)
let request: AbortController | undefined
let version = 0
let observer: ResizeObserver | undefined

function place() {
  const anchor = accountHover.value?.anchor
  if (!anchor?.isConnected || !card.value) { closeAccountHover(); return }
  const rect = anchor.getBoundingClientRect()
  const { width, height } = card.value.getBoundingClientRect()
  const gap = 10
  const top = rect.bottom + gap + height <= window.innerHeight - 8 ? rect.bottom + gap : rect.top - gap - height
  position.value = {
    left: `${Math.max(8, Math.min(rect.left, window.innerWidth - width - 8))}px`,
    top: `${Math.max(8, Math.min(top, window.innerHeight - height - 8))}px`, visibility: 'visible',
  }
}

watch(accountHover, async target => {
  request?.abort()
  const current = ++version
  request = new AbortController()
  const signal = request.signal
  account.value = target?.account
  relationship.value = undefined
  failed.value = followFailed.value = headerFailed.value = busy.value = false
  loading.value = !!target && !account.value
  await nextTick()
  if (current !== version || !target) return
  place()
  try {
    const result = target.account ?? await userApi.lookupCachedAccount(target.acct, signal)
    if (signal.aborted || current !== version) return
    account.value = result
    loading.value = false
    if (auth.isAuthenticated && auth.currentUser?.id !== result.id) {
      const rels = await userApi.getRelationships([result.id], signal)
      if (!signal.aborted && current === version) relationship.value = rels.find(rel => rel.id === result.id)
    }
  } catch {
    if (!signal.aborted && current === version) failed.value = true
  } finally {
    if (!signal.aborted && current === version) { loading.value = false; await nextTick(); place() }
  }
}, { immediate: true })

async function toggleFollow() {
  if (!canFollow.value || busy.value || !account.value) return
  const current = version
  const epoch = sessionEpoch.value
  const id = account.value.id
  const wasFollowing = !!relationship.value?.following
  busy.value = true
  followFailed.value = false
  try {
    const rel = state.value === 'none' ? await userApi.follow(id) : await userApi.unfollow(id)
    if (current !== version || epoch !== sessionEpoch.value) return
    relationship.value = rel
    if (account.value?.id === id) account.value = { ...account.value, followers_count: Math.max(0, account.value.followers_count + Number(rel.following) - Number(wasFollowing)) }
  } catch {
    if (current === version && epoch === sessionEpoch.value) followFailed.value = true
  } finally { if (current === version) busy.value = false }
}
function dismiss(event: KeyboardEvent) {
  if (event.key !== 'Escape') return
  if (card.value?.contains(document.activeElement)) accountHover.value?.anchor.focus({ preventScroll: true })
  closeAccountHover()
}
function outside(event: Event) {
  const target = event.target as Node
  if (!card.value?.contains(target) && !accountHover.value?.anchor.contains(target)) closeAccountHover()
}
function scroll(event: Event) {
  if (!card.value?.contains(event.target as Node)) closeAccountHover()
}
function onFocusOut(event: FocusEvent) {
  if (!card.value?.contains(event.relatedTarget as Node)) leaveAccountHover()
}
function onTab(event: KeyboardEvent) {
  if (event.key !== 'Tab' || !card.value) return
  const links = [...card.value.querySelectorAll<HTMLElement>('a, button:not(:disabled)')]
  if ((event.shiftKey && event.target === links[0]) || (!event.shiftKey && event.target === links.at(-1))) {
    accountHover.value?.anchor.focus({ preventScroll: true })
    closeAccountHover()
    // Shift-Tab returns to the trigger; forward Tab resumes the normal page order.
    if (event.shiftKey) event.preventDefault()
  }
}
watch(() => route.fullPath, closeAccountHover)
onMounted(() => {
  observer = new ResizeObserver(place)
  if (card.value) observer.observe(card.value)
  document.addEventListener('keydown', dismiss)
  document.addEventListener('pointerdown', outside, true)
  window.addEventListener('scroll', scroll, true)
  window.addEventListener('resize', closeAccountHover)
})
onBeforeUnmount(() => {
  version++
  request?.abort()
  observer?.disconnect()
  document.removeEventListener('keydown', dismiss)
  document.removeEventListener('pointerdown', outside, true)
  window.removeEventListener('scroll', scroll, true)
  window.removeEventListener('resize', closeAccountHover)
})
</script>

<template>
  <Teleport to="body">
    <section id="account-hover-card" ref="card" class="account-hover-card" role="dialog" :aria-label="$t('shared.AccountHoverCard.title')" :style="position"
      @pointerenter="keepAccountHover" @pointerleave="leaveAccountHover" @focusin="keepAccountHover" @focusout="onFocusOut" @keydown="onTab"
>
      <template v-if="account">
        <div class="account-hover-card__cover">
          <img v-if="account.header && !headerFailed" :src="account.header" alt="" @error="headerFailed = true" />
          <span v-if="relationship?.followed_by" class="account-hover-card__badge">{{ $t('user.ProfilePage.s4') }}</span>
        </div>
        <div class="account-hover-card__profile">
          <div class="account-hover-card__actions">
            <router-link :to="profilePath" class="account-hover-card__avatar" :aria-label="account.display_name || account.username"><UserAvatar :account="account" :size="64" /></router-link>
            <button v-if="canFollow" type="button" class="btn btn--sm" :class="state === 'none' ? 'btn--primary' : 'btn--secondary'" :disabled="busy" @click="toggleFollow">
              {{ busy ? $t('shared.AccountHoverCard.saving') : followButtonLabel(state, relationship?.followed_by) }}
            </button>
          </div>
          <router-link :to="profilePath" class="account-hover-card__identity">
            <strong dir="auto"><EmojiText :text="account.display_name || account.username" :emojis="account.emojis" /></strong>
            <span dir="ltr">@{{ displayAccount(account.acct) }}</span>
          </router-link>
          <RichText v-if="account.note" :html="account.note" :emojis="account.emojis" :account-hover-enabled="false" class="account-hover-card__bio" />
          <p v-if="followFailed" class="account-hover-card__error" role="alert">{{ $t('shared.AccountHoverCard.followError') }}</p>
          <p v-else-if="failed" class="account-hover-card__error" role="status">{{ $t('shared.AccountHoverCard.relationshipError') }}</p>
        </div>
        <nav class="account-hover-card__stats">
          <router-link :to="profilePath"><strong>{{ formatCount(account.statuses_count) }}</strong><span>{{ $t('shared.routes.s14') }}</span></router-link>
          <router-link :to="`${profilePath}/following`"><strong>{{ formatCount(account.following_count) }}</strong><span>{{ $t('shared.routes.s17') }}</span></router-link>
          <router-link :to="`${profilePath}/followers`"><strong>{{ formatCount(account.followers_count) }}</strong><span>{{ $t('shared.routes.s16') }}</span></router-link>
        </nav>
      </template>
      <div v-else class="account-hover-card__empty" :aria-busy="loading">
        <p role="status">{{ $t(loading ? 'shared.AccountHoverCard.loading' : 'shared.AccountHoverCard.unavailable') }}</p>
        <router-link :to="profilePath">@{{ displayAccount(accountHover?.acct || '') }}</router-link>
      </div>
    </section>
  </Teleport>
</template>

<style scoped>
.account-hover-card { position: fixed; z-index: var(--z-popover); width: min(344px, calc(100vw - 16px)); max-height: calc(100dvh - 16px); overflow: auto; overscroll-behavior: contain; border: var(--hairline) solid var(--color-separator); border-radius: var(--radius-lg); background: var(--color-bg-elevated); box-shadow: var(--shadow-popover); color: var(--color-label); font-size: var(--text-subhead); line-height: 1.5; }
.account-hover-card a { color: inherit; text-decoration: none; }
.account-hover-card__cover { height: 100px; position: relative; background: var(--color-accent-soft); }
.account-hover-card__cover img { width: 100%; height: 100%; object-fit: cover; }
.account-hover-card__badge { position: absolute; top: 10px; left: 12px; padding: 2px 8px; border-radius: var(--radius-full); background: var(--color-bg-elevated); font-size: var(--text-caption); }
.account-hover-card__profile { padding: 0 18px 16px; }
.account-hover-card__actions { display: flex; justify-content: space-between; align-items: flex-end; min-height: 46px; gap: 12px; margin-bottom: 10px; }
.account-hover-card__avatar { display: inline-flex; margin-top: -28px; border: 4px solid var(--color-bg-elevated); border-radius: 50%; position: relative; }
.account-hover-card__identity { display: flex; flex-direction: column; overflow-wrap: anywhere; }
.account-hover-card__identity strong { font-size: var(--text-body); line-height: 1.4; }
.account-hover-card__identity > span { color: var(--color-label-secondary); font-size: var(--text-footnote); }
.account-hover-card__bio { margin-top: 12px; max-height: 8.5em; overflow: auto; }
.account-hover-card__stats { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); border-top: var(--hairline) solid var(--color-separator); padding: 12px; gap: 6px; }
.account-hover-card__stats a { display: flex; flex-direction: column; align-items: center; min-width: 0; padding: 4px; border-radius: var(--radius-sm); }
.account-hover-card__stats span { color: var(--color-label-secondary); font-size: var(--text-caption); }
.account-hover-card__stats strong { font-size: var(--text-body); font-variant-numeric: tabular-nums; }
.account-hover-card__stats a:hover { background: var(--color-hover); }
.account-hover-card__empty { padding: 24px; display: grid; gap: 12px; overflow-wrap: anywhere; }
.account-hover-card__empty a { color: var(--color-accent-text); }
.account-hover-card__error { margin-top: 12px; color: var(--color-label-secondary); font-size: var(--text-footnote); }
</style>
