<script setup lang="ts">
import { displayAccount } from '@/shared/utils/accountDisplay'
import EmojiText from '@/shared/components/common/EmojiText.vue'
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { useRoute } from 'vue-router'
import { MessagePlugin, DialogPlugin } from '@/shared/ui/feedback'
import type { MastodonAccount, MastodonStatus } from '@/shared/types/mastodon'
import { userApi, type ProfileBackfillState } from '../../infrastructure/userApi'
import { PROFILE_TABS, type ProfileTabValue } from '../../domain/entities'
import { followButtonLabel } from '../../domain/relationship'
import { useRelationships } from '../composables/useRelationships'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { useStatusActions, applyStatusUpdate } from '@/domains/post/presentation/composables/useStatusActions'
import { usePaginatedList } from '@/shared/composables/usePaginatedList'
import type { CursorPage } from '@/shared/utils/pagination'
import { TIMELINE_PAGE_SIZE } from '@/shared/utils/constants'
import TimelineFeed from '@/domains/timeline/presentation/components/TimelineFeed.vue'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import { formatCount } from '@/shared/utils/format'
import RichText from '@/shared/components/common/RichText.vue'
import TabBar from '@/shared/components/common/TabBar.vue'
import AddToListButton from '@/domains/list/presentation/components/AddToListButton.vue'
import ReportDialog from '../components/ReportDialog.vue'
import ProfileMediaGrid from '../components/ProfileMediaGrid.vue'

import { t } from '@/shared/i18n'
const route = useRoute()
const authStore = useAuthStore()
const account = ref<MastodonAccount | null>(null)
const activeTab = ref<ProfileTabValue>('posts')
const loading = ref(true)
const error = ref<string | null>(null)
const relationships = useRelationships()
const reporting = ref(false)

// Request version guard — a slow lookup for a previous profile must not
// overwrite the profile we navigated to.
let requestVersion = 0

const isOwnProfile = computed(() => !!account.value && account.value.id === authStore.currentUser?.id)
const visibleTabs = computed(() => PROFILE_TABS.filter(t => !t.ownOnly || isOwnProfile.value))
const relationship = computed(() => account.value ? relationships.rels.get(account.value.id) : undefined)
const followState = computed(() => account.value ? relationships.state(account.value.id) : 'unknown')
const profileBase = computed(() => `/@${route.params.username as string}`)

// Remote profiles: the server imports older posts from the remote outbox in
// the background and reports its progress on the first page. While it is
// pending the first page is reloaded a couple of times.
const backfill = ref<ProfileBackfillState>(null)
const BACKFILL_POLL_MS = 2500
// The server pages through the remote outbox politely (several pages,
// ~300ms apart), so poll a few times before giving up.
const BACKFILL_MAX_POLLS = 5
let backfillPolls = 0
let backfillTimer: ReturnType<typeof setTimeout> | undefined
const isRemote = computed(() => !!account.value?.acct.includes('@'))
const remoteProfileUrl = computed(() => {
  const url = account.value?.url
  return url && /^https?:\/\//i.test(url) ? url : ''
})

const backfillPendingEmpty = computed(() => isRemote.value && activeTab.value !== 'likes' && backfill.value === 'pending' && statuses.value.length === 0)

function clearBackfillPoll() {
  if (backfillTimer) clearTimeout(backfillTimer)
  backfillTimer = undefined
}

function resetBackfill() {
  clearBackfillPoll()
  backfill.value = null
  backfillPolls = 0
}

async function fetchStatuses(maxId: string | undefined, limit: number): Promise<MastodonStatus[] | CursorPage<MastodonStatus>> {
  const acct = account.value
  if (!acct) return []
  // Likes use the relationship cursor supplied by the server's Link header.
  if (activeTab.value === 'likes') return userApi.getFavourites({ max_id: maxId, limit })
  const params = {
    max_id: maxId,
    limit,
    ...(activeTab.value === 'posts' ? { exclude_replies: true } : {}),
    ...(activeTab.value === 'media' ? { only_media: true } : {}),
  }
  if (maxId || !isRemote.value) return userApi.getStatuses(acct.id, params)
  const { statuses: page, backfill: state } = await userApi.getStatusesWithBackfill(acct.id, params)
  if (account.value?.id !== acct.id) return page
  // A cooldown answer ("done"/"unavailable") after polling is final.
  if (state || backfillPolls === 0) backfill.value = state
  clearBackfillPoll()
  if (state === 'pending' && backfillPolls < BACKFILL_MAX_POLLS) {
    backfillTimer = setTimeout(() => {
      backfillTimer = undefined
      if (account.value?.id !== acct.id) return
      backfillPolls++
      void feed.refresh()
    }, BACKFILL_POLL_MS)
  } else if (state === 'pending') {
    backfill.value = 'done'
  }
  return page
}

const feed = usePaginatedList(fetchStatuses, TIMELINE_PAGE_SIZE)
const { items: statuses } = feed

const { toggleReblog, toggleFavourite, toggleBookmark } = useStatusActions(
  (id, updates) => applyStatusUpdate(statuses.value, id, updates),
)

async function loadProfile() {
  const myVersion = ++requestVersion
  loading.value = true
  error.value = null
  account.value = null
  resetBackfill()
  feed.reset()
  activeTab.value = 'posts'
  const username = route.params.username as string
  try {
    const acctData = await userApi.lookupAccount(username)
    if (myVersion !== requestVersion) return
    account.value = acctData
    feed.refresh()
    if (acctData.id !== authStore.currentUser?.id) relationships.load([acctData.id])
  } catch (e) {
    if (myVersion !== requestVersion) return
    console.error('Failed to load profile:', e)
    error.value = e instanceof Error ? e.message : t('user.ProfilePage.s22')
  } finally {
    if (myVersion === requestVersion) loading.value = false
  }
}

async function toggleFollow() {
  if (!account.value) return
  const targetId = account.value.id
  const wasFollowing = relationships.state(targetId) === 'following'
  try {
    await relationships.toggleFollow(targetId)
    // Keep the follower count in step with the button.
    if (account.value?.id === targetId) {
      const following = relationships.state(targetId) === 'following'
      account.value.followers_count = Math.max(0, account.value.followers_count + Number(following) - Number(wasFollowing))
    }
  } catch (e) {
    MessagePlugin.error(e instanceof Error ? e.message : t('user.ProfilePage.s23'))
  }
}

async function applyRelation(next: Promise<import('@/shared/types/mastodon').MastodonRelationship>) {
  if (!account.value) return
  const rel = await next
  relationships.rels.set(account.value.id, rel)
}

function toggleMute() {
  if (!account.value || !relationship.value) return
  const id = account.value.id
  const muting = relationship.value.muting
  void applyRelation(muting ? userApi.unmute(id) : userApi.mute(id)).catch((e) => {
    MessagePlugin.error(e instanceof Error ? e.message : t('user.ProfilePage.s23'))
  })
}

function toggleBlock() {
  if (!account.value || !relationship.value) return
  const id = account.value.id
  const blocking = relationship.value.blocking
  const dialog = DialogPlugin.confirm({
    header: blocking ? t('user.ProfilePage.s24') : t('user.ProfilePage.s25'),
    body: blocking ? t('user.ProfilePage.s26') : t('user.ProfilePage.s27'),
    confirmBtn: blocking ? t('user.ProfilePage.s8') : { content: t('user.ProfilePage.s9'), theme: 'danger' },
    cancelBtn: t('user.ProfilePage.s28'),
    onConfirm: async () => {
      try {
        await applyRelation(blocking ? userApi.unblock(id) : userApi.block(id))
        MessagePlugin.success(blocking ? t('user.ProfilePage.s29') : t('user.ProfilePage.s30'))
      } catch (e) {
        MessagePlugin.error(e instanceof Error ? e.message : t('user.ProfilePage.s23'))
      } finally {
        dialog.destroy()
      }
    },
    onClose: () => dialog.destroy(),
  })
}

onMounted(loadProfile)
watch(() => route.params.username, loadProfile)
onBeforeUnmount(clearBackfillPoll)
watch(activeTab, () => {
  // A different tab is a different query, not a refresh of the visible rows.
  clearBackfillPoll()
  feed.reset()
  if (account.value) void feed.refresh()
}, { flush: 'sync' })
</script>

<template>
  <div class="profile-page" v-if="account">
    <!-- Header -->
    <div class="page-header page-header--with-back">
      <button
        class="page-header__back-btn"
        type="button"
        @click="$router.back()"
        :aria-label="$t('user.ProfilePage.s1')"
      >
        <t-icon name="chevron-left" :size="20" />
      </button>
      <div>
        <h1 class="page-header__title" dir="auto"><EmojiText :text="account.display_name || account.username" :emojis="account.emojis" /></h1>
        <span class="profile-header-bar__count">{{ $t('user.ProfilePage.s2', [formatCount(account.statuses_count)]) }}</span>
      </div>
    </div>

    <!-- Banner -->
    <div class="profile-banner">
      <img v-if="account.header" :src="account.header" alt="" class="profile-banner__img" loading="lazy" />
      <div v-else class="profile-banner__placeholder" />
    </div>

    <!-- Avatar + Info -->
    <div class="profile-info">
      <div class="profile-info__top">
        <UserAvatar :account="account" :size="128" class="profile-avatar" />
        <t-button v-if="isOwnProfile" theme="default" shape="round" class="edit-btn" @click="$router.push('/settings')">
          {{ $t('user.ProfilePage.s3') }}
        </t-button>
        <t-button
          v-else-if="followState !== 'unknown'"
          theme="default"
          shape="round"
          class="edit-btn"
          :class="{ 'edit-btn--following': followState !== 'none' }"
          :loading="relationships.pending.has(account.id)"
          @click="toggleFollow"
        >
          {{ followButtonLabel(followState, relationship?.followed_by) }}
        </t-button>
      </div>

      <h2 class="profile-name" dir="auto"><EmojiText :text="account.display_name || account.username" :emojis="account.emojis" /></h2>
      <div class="profile-username">
        @{{ displayAccount(account.acct) }}
        <span v-if="relationship?.followed_by" class="profile-follows-you">{{ $t('user.ProfilePage.s4') }}</span>
      </div>
      <AddToListButton v-if="!isOwnProfile && followState === 'following'" :account-id="account.id" />
      <div v-if="!isOwnProfile && followState !== 'unknown'" class="profile-relations">
        <router-link v-if="!relationship?.blocking && !relationship?.blocked_by" class="btn btn--ghost btn--sm" :to="{ path: '/messages', query: { to: account.acct } }">{{ $t('user.ProfilePage.s5') }}</router-link>
        <button type="button" class="btn btn--ghost btn--sm" @click="toggleMute">{{ relationship?.muting ? $t('user.ProfilePage.s6') : $t('user.ProfilePage.s7') }}</button>
        <button type="button" class="btn btn--ghost btn--sm" @click="toggleBlock">{{ relationship?.blocking ? $t('user.ProfilePage.s8') : $t('user.ProfilePage.s9') }}</button>
        <button type="button" class="btn btn--ghost btn--sm" @click="reporting = true">{{ $t('user.ProfilePage.s10') }}</button>
      </div>

      <RichText v-if="account.note" class="profile-bio" :html="account.note" :emojis="account.emojis" />

      <dl v-if="account.fields?.length" class="profile-fields">
        <div v-for="(field, i) in account.fields" :key="i" class="profile-field" :class="{ 'profile-field--verified': field.verified_at }">
          <dt dir="auto"><EmojiText :text="field.name" :emojis="account.emojis" /></dt>
          <dd><RichText :html="field.value" :emojis="account.emojis" /></dd>
        </div>
      </dl>

      <div class="profile-stats">
        <router-link :to="`${profileBase}/following`" class="stat-item">
          <strong>{{ formatCount(account.following_count) }}</strong>
          <span> {{ $t('user.ProfilePage.s11') }}</span>
        </router-link>
        <router-link :to="`${profileBase}/followers`" class="stat-item">
          <strong>{{ formatCount(account.followers_count) }}</strong>
          <span> {{ $t('user.ProfilePage.s12') }}</span>
        </router-link>
      </div>
    </div>

    <!-- Tabs -->
    <TabBar
      v-model="activeTab"
      :options="visibleTabs"
      :label="$t('user.ProfilePage.s13')"
      style="margin-top: var(--space-4)"
    />

    <!-- Remote profile backfill -->
    <div
      v-if="backfillPendingEmpty"
      class="profile-backfill"
      role="status"
      aria-live="polite"
      data-testid="profile-backfill-pending"
    >
      <t-loading size="small" />
      <span>{{ $t('user.ProfilePage.s14') }}</span>
    </div>
    <div
      v-else-if="isRemote && activeTab !== 'likes' && backfill === 'unavailable' && statuses.length === 0 && !feed.loading.value"
      class="profile-backfill profile-backfill--hint"
      data-testid="profile-backfill-unavailable"
    >
      <span>{{ $t('user.ProfilePage.s15') }}</span>
      <a v-if="remoteProfileUrl" :href="remoteProfileUrl" target="_blank" rel="noopener noreferrer nofollow" class="btn btn--secondary btn--sm">{{ $t('user.ProfilePage.s16') }}</a>
    </div>
    <div
      v-else-if="isRemote && activeTab !== 'likes' && backfill === 'done' && statuses.length === 0 && !feed.loading.value && !feed.hasMore.value && remoteProfileUrl"
      class="profile-backfill profile-backfill--hint"
      data-testid="profile-backfill-origin"
    >
      <span>{{ $t('user.ProfilePage.olderOnOrigin') }}</span>
      <a :href="remoteProfileUrl" target="_blank" rel="noopener noreferrer nofollow" class="btn btn--secondary btn--sm">{{ $t('user.ProfilePage.olderOnOriginLink') }}</a>
    </div>

    <!-- Posts -->
    <template v-if="backfillPendingEmpty" />
    <ProfileMediaGrid
      v-else-if="activeTab === 'media'"
      :statuses="statuses"
      :loading="feed.loading.value"
      :has-more="feed.hasMore.value"
      :error="feed.error.value"
      @load-more="feed.loadMore"
      @retry="feed.refresh()"
    />
    <TimelineFeed
      v-else
      :statuses="statuses"
      :loading="feed.loading.value"
      :has-more="feed.hasMore.value"
      :error="feed.error.value"
      :empty-title="$t('user.ProfilePage.s17')"
      :empty-text="activeTab === 'likes' ? $t('user.ProfilePage.s18') : $t('user.ProfilePage.s19')"
      @load-more="feed.loadMore"
      @retry="statuses.length && feed.hasMore.value ? feed.loadMore() : feed.refresh()"
      @reblog="toggleReblog"
      @favourite="toggleFavourite"
      @bookmark="toggleBookmark"
    />
    <ReportDialog v-if="reporting" v-model="reporting" :account-id="account.id" />
  </div>

  <div v-else-if="loading" class="page-loading">
    <t-loading />
  </div>

  <div v-else-if="error" class="empty-state" role="alert">
    <div class="empty-state__icon">
      <t-icon name="error-circle" :size="48" />
    </div>
    <h2 class="empty-state__title">{{ $t('user.ProfilePage.s20') }}</h2>
    <p class="empty-state__text">{{ error }}</p>
    <button type="button" class="btn btn--secondary btn--sm" @click="loadProfile">{{ $t('user.ProfilePage.s21') }}</button>
  </div>
</template>

<style scoped>
.profile-backfill {
  display: flex;
  align-items: center;
  justify-content: center;
  flex-wrap: wrap;
  gap: var(--space-2);
  padding: var(--space-4);
  font-size: var(--text-footnote, 13px);
  color: var(--color-label-secondary);
  text-align: center;
}

.profile-page {
  min-height: 100dvh;
}

.page-header--with-back > div {
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.page-header--with-back .page-header__title {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.profile-header-bar__count {
  font-size: var(--text-caption);
  letter-spacing: var(--tracking-caption);
  color: var(--color-label-secondary);
  font-variant-numeric: tabular-nums;
}

.profile-banner {
  aspect-ratio: 3 / 1;
  max-height: 12.5rem;
  overflow: hidden;
  background: var(--color-fill);
}

.profile-banner__img {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.profile-banner__placeholder {
  width: 100%;
  height: 100%;
  background: linear-gradient(135deg, var(--color-fill), var(--color-fill-strong));
}

.profile-info {
  padding: 0 var(--gutter);
}

.profile-info__top {
  display: flex;
  justify-content: space-between;
  align-items: flex-end;
  gap: var(--space-3);
  margin-top: calc(-1 * var(--profile-avatar) / 2);
  --profile-avatar: 6.5rem;
}

/* Avatar sits on the banner edge with a ring in the page colour. */
.profile-avatar {
  width: var(--profile-avatar) !important;
  height: var(--profile-avatar) !important;
  font-size: calc(var(--profile-avatar) * 0.4) !important;
  border-radius: 50%;
  background: var(--color-bg);
  box-shadow: 0 0 0 4px var(--color-bg), var(--shadow-1);
}

/* The frame is round too, so the ring follows the avatar, not a square. */
.profile-avatar :deep(.user-avatar) {
  width: 100% !important;
  height: 100% !important;
}

/* Follow is the one prominent action: a label-coloured capsule.
   Once following it recedes into a quiet outlined capsule. */
.edit-btn {
  margin-bottom: var(--space-1);
  min-width: 6rem;
  font-weight: var(--weight-semibold) !important;
  --td-bg-color-component: var(--color-label);
  background: var(--color-label) !important;
  border-color: var(--color-label) !important;
  color: var(--color-bg) !important;
}

.edit-btn--following {
  background: transparent !important;
  border-color: var(--color-separator-opaque) !important;
  color: var(--color-label) !important;
}

.profile-name {
  margin-top: var(--space-3);
  font-size: var(--text-title-2);
  line-height: var(--leading-title-2);
  letter-spacing: var(--tracking-title-2);
  font-weight: var(--weight-bold);
  color: var(--color-label);
  overflow-wrap: break-word;
  unicode-bidi: plaintext;
}

.profile-username {
  color: var(--color-label-secondary);
  font-size: var(--text-subhead);
  direction: ltr;
  unicode-bidi: isolate;
}

.profile-bio {
  margin-top: var(--space-3);
  font-size: var(--text-body);
  line-height: var(--leading-body);
  color: var(--color-label);
}

.profile-bio:lang(zh) {
  line-height: var(--leading-cjk);
}

.profile-fields {
  display: grid;
  margin-top: var(--space-3);
  border-radius: var(--radius-md);
  background: var(--color-fill);
  overflow: hidden;
}

.profile-field {
  display: grid;
  grid-template-columns: minmax(0, 2fr) minmax(0, 3fr);
  gap: var(--space-3);
  padding: var(--space-2) var(--space-3);
  font-size: var(--text-subhead);
  line-height: var(--leading-subhead);
}

.profile-field + .profile-field {
  border-top: var(--hairline) solid var(--color-separator);
}

.profile-field dt {
  color: var(--color-label-secondary);
  overflow-wrap: anywhere;
}

.profile-field dd {
  margin: 0;
  min-width: 0;
  color: var(--color-label);
}

.profile-field--verified dd {
  color: var(--color-success, var(--color-label));
}

.profile-stats {
  display: flex;
  gap: var(--space-5);
  margin-top: var(--space-3);
}

.stat-item {
  display: inline-flex;
  gap: var(--space-1);
  font-size: var(--text-subhead);
  color: var(--color-label-secondary);
}

.profile-follows-you {
  margin-left: var(--space-2);
  padding: 0 var(--space-2);
  border-radius: var(--radius-sm, 4px);
  background: var(--color-fill, var(--bg-secondary));
  font-size: var(--font-xs);
}

.profile-relations {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin-top: var(--space-3);
}

.stat-item strong {
  color: var(--color-label);
  font-weight: var(--weight-semibold);
  font-variant-numeric: tabular-nums;
}

@media (min-width: 43.75rem) {
  .profile-info__top {
    --profile-avatar: 8rem;
  }
}
</style>
