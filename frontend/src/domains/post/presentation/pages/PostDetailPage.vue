<script setup lang="ts">
import { displayAccount } from '@/shared/utils/accountDisplay'
import { wasEdited } from '@/shared/utils/format'
import PostReactions from '@/shared/components/post/PostReactions.vue'
import EmojiText from '@/shared/components/common/EmojiText.vue'
import { ref, computed, onMounted, onBeforeUnmount, watch, nextTick } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import type { MastodonStatus } from '@/shared/types/mastodon'
import { postApi } from '../../infrastructure/postApi'
import { canBoost, visibilityOption, DIRECT_SCOPE } from '../../domain/compose'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { useUiChrome } from '@/shared/composables/useUiChrome'
import { MessagePlugin } from '@/shared/ui/feedback'
import ReportDialog from '@/domains/user/presentation/components/ReportDialog.vue'
import {
  useStatusActions, applyStatusUpdate, deletedStatusIds, editedStatuses,
} from '../../presentation/composables/useStatusActions'
import PostCard from '@/shared/components/post/PostCard.vue'
import AccountLink from '@/shared/components/common/AccountLink.vue'
import PostContent from '@/shared/components/post/PostContent.vue'
import PostActions from '@/shared/components/post/PostActions.vue'
import PostComposer from '@/shared/components/post/PostComposer.vue'
import FilteredNotice from '@/shared/components/post/FilteredNotice.vue'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { mergeById } from '@/shared/utils/pagination'

import { t } from '@/shared/i18n'
const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const { openQuote } = useUiChrome()
const reporting = ref(false)

const status = ref<MastodonStatus | null>(null)
const ancestors = ref<MastodonStatus[]>([])
const descendants = ref<MastodonStatus[]>([])
const loading = ref(true)
const error = ref<string | null>(null)
// The author requires sign-in to view their posts (visitor only).
const signinRequired = ref(false)
const contextLoading = ref(false)
const contextError = ref<string | null>(null)
const replyComposer = ref<InstanceType<typeof PostComposer>>()

// The status actually shown: the reblogged original if the id is a reblog,
// with any edit made in this session applied.
const mainStatus = computed(() => {
  const base = status.value?.reblog ?? status.value
  if (!base) return null
  const edited = editedStatuses.get(base.id)
  return edited ? { ...base, ...edited } : base
})
const heading = computed(() => mainStatus.value?.visibility === 'direct' ? t('post.PostDetailPage.s15') : t('post.PostDetailPage.s16'))
const isOwn = computed(() => !!mainStatus.value && mainStatus.value.account.id === authStore.currentUser?.id)
const visibility = computed(() => {
  const v = mainStatus.value?.visibility
  return v && v !== 'public' ? visibilityOption(v) : null
})
const filterMarks = computed(() => {
  const marks = new Set<NonNullable<MastodonStatus['filtered']>[number]>()
  for (const item of [status.value, mainStatus.value]) {
    for (const mark of item?.filtered ?? []) marks.add(mark)
  }
  return [...marks]
})

// All status arrays (ancestors + descendants + main status) need updates;
// the helper applies to whichever array contains the matching id.
const allLists = () => [ancestors.value, descendants.value]
const { toggleReblog, toggleFavourite, toggleBookmark } = useStatusActions((id, updates) => {
  for (const list of allLists()) applyStatusUpdate(list, id, updates)
  if (status.value?.id === id) {
    Object.assign(status.value, updates)
  } else if (status.value?.reblog?.id === id) {
    Object.assign(status.value.reblog, updates)
  }
})

// Version guard — prevents a stale response (from navigating between
// posts quickly) from overwriting newer post data.
let requestVersion = 0
let contextVersion = 0

async function loadContext() {
  if (contextLoading.value) return
  const version = requestVersion, contextRequest = ++contextVersion
  const statusId = route.params.statusId as string
  const current = () => version === requestVersion && contextRequest === contextVersion
  contextLoading.value = true
  contextError.value = null
  try {
    const data = await postApi.getContext(statusId)
    if (!current()) return
    ancestors.value = data.ancestors
    // A reply sent while the context request was pending is already visible.
    descendants.value = mergeById(data.descendants, descendants.value)
  } catch (e) {
    if (current()) contextError.value = e instanceof Error ? e.message : t('post.PostDetailPage.s17')
  } finally {
    if (current()) contextLoading.value = false
  }
}

async function loadPost() {
  const myVersion = ++requestVersion
  const statusId = route.params.statusId as string
  loading.value = true
  error.value = null
  signinRequired.value = false
  status.value = null
  ancestors.value = []; descendants.value = []
  contextLoading.value = false; contextError.value = null
  void loadContext()
  try {
    const statusData = await postApi.getStatus(statusId)
    if (myVersion !== requestVersion) return
    status.value = statusData
  } catch (e) {
    if (myVersion !== requestVersion) return
    console.error('Failed to load post:', e)
    const code = (e as { response?: { status?: number } })?.response?.status
    if (code === 401 && (e as { response?: { data?: { code?: string } } })?.response?.data?.code === 'SIGNIN_REQUIRED') {
      signinRequired.value = true
      return
    }
    error.value = code === 404 ? t('post.PostDetailPage.s18') : code === 403 ? t('post.PostDetailPage.s19') : t('post.PostDetailPage.s20')
    status.value = null
  } finally {
    if (myVersion === requestVersion) {
      loading.value = false
    }
  }
  if (myVersion === requestVersion && status.value && route.query.reply) {
    await nextTick()
    replyComposer.value?.focus()
  }
}

onMounted(loadPost)
onBeforeUnmount(() => { ++requestVersion; ++contextVersion })
watch(() => route.params.statusId, (id) => { if (id) loadPost() })
watch(sessionEpoch, () => { void loadPost() }, { flush: 'sync' })
watch(heading, (title) => { document.title = `${title} - Community` })

// Leave the page when the main post is deleted from its menu.
watch(() => mainStatus.value && deletedStatusIds.has(mainStatus.value.id), (gone) => {
  if (gone) goBack()
})

function onReplied(reply: MastodonStatus) {
  descendants.value.push(reply)
  if (status.value) {
    const target = status.value.reblog ?? status.value
    target.replies_count = (target.replies_count || 0) + 1
  }
}

function focusReply() {
  replyComposer.value?.focus()
}

function goBack() {
  // Check if there's history to go back to, otherwise go to home
  if (window.history.state?.back) {
    router.back()
  } else {
    router.push({ name: 'home' })
  }
}

async function togglePin() {
  if (!mainStatus.value) return
  try {
    const updated = mainStatus.value.pinned ? await postApi.unpin(mainStatus.value.id) : await postApi.pin(mainStatus.value.id)
    status.value = updated
    MessagePlugin.success(updated.pinned ? t('post.PostDetailPage.s21') : t('post.PostDetailPage.s22'))
  } catch (e) {
    MessagePlugin.error(e instanceof Error && e.message ? e.message : t('post.PostDetailPage.s23'))
  }
}
</script>

<template>
  <div class="detail-page">
    <div class="page-header page-header--with-back">
      <button class="page-header__back-btn" @click="goBack" :aria-label="$t('post.PostDetailPage.s1')">
        <t-icon name="chevron-left" :size="20" />
      </button>
      <h1 class="page-header__title">{{ heading }}</h1>
    </div>

    <div v-if="loading" class="page-loading">
      <t-loading />
    </div>

    <div v-else-if="signinRequired" class="empty-state" data-testid="post-signin-required">
      <h2 class="empty-state__title">{{ $t('user.ProfilePage.signinRequired') }}</h2>
      <p class="empty-state__text">{{ $t('user.ProfilePage.signinRequiredHint') }}</p>
      <router-link :to="{ path: '/login', query: { redirect: route.fullPath } }" class="btn btn--primary">{{ $t('user.ProfilePage.signinRequiredAction') }}</router-link>
    </div>

    <div v-else-if="error" class="empty-state" role="alert">
      <div class="empty-state__icon">
        <t-icon name="error-circle" :size="48" />
      </div>
      <h2 class="empty-state__title">{{ $t('post.PostDetailPage.s2') }}</h2>
      <p class="empty-state__text">{{ error }}</p>
      <button type="button" class="btn btn--secondary btn--sm" @click="loadPost">{{ $t('post.PostDetailPage.s3') }}</button>
    </div>

    <template v-else-if="status && mainStatus">
      <!-- Ancestors (context above) -->
      <PostCard
        v-for="s in ancestors"
        :key="s.id"
        :status="s"
        :show-reply-context="false"
        @reblog="toggleReblog"
        @favourite="toggleFavourite"
        @bookmark="toggleBookmark"
      />

      <!-- Main post (highlighted) -->
      <article class="detail-main" :aria-label="$t('post.PostDetailPage.s4')">
        <div class="detail-main__header">
          <AccountLink :account="mainStatus.account" class="detail-main__avatar" :aria-label="$t('post.PostDetailPage.s5', [mainStatus.account.display_name || mainStatus.account.username])">
            <UserAvatar :account="mainStatus.account" :size="48" />
          </AccountLink>
          <div class="detail-main__user">
            <AccountLink :account="mainStatus.account" class="detail-main__name" :title="mainStatus.account.display_name || mainStatus.account.username"><EmojiText :text="mainStatus.account.display_name || mainStatus.account.username" :emojis="mainStatus.account.emojis" /></AccountLink>
            <AccountLink :account="mainStatus.account" class="detail-main__username">@{{ displayAccount(mainStatus.account.acct) }}</AccountLink>
          </div>
        </div>

        <FilteredNotice :marks="filterMarks">
          <PostContent :status="mainStatus" text-class="detail-main__content rich-text" />

          <div class="detail-main__time">
            <span v-if="visibility" class="detail-main__visibility" :title="visibility.label">
              <t-icon :name="visibility.icon" :size="14" />
              {{ visibility.label }}
              <span aria-hidden="true"> · </span>
            </span>
            <time :datetime="mainStatus.created_at">
              {{ new Date(mainStatus.created_at).toLocaleString(undefined, { year: 'numeric', month: 'long', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false }) }}
            </time>
            <span v-if="wasEdited(mainStatus)"> {{ $t('post.PostDetailPage.s6') }}</span>
            <button v-if="isOwn" type="button" class="btn btn--ghost btn--sm" @click="togglePin">{{ mainStatus.pinned ? $t('post.PostDetailPage.s7') : $t('post.PostDetailPage.s8') }}</button>
            <button v-else-if="authStore.currentUser" type="button" class="btn btn--ghost btn--sm" @click="reporting = true">{{ $t('post.PostDetailPage.s9') }}</button>
          </div>
          <p v-if="mainStatus.visibility === 'direct'" class="detail-main__scope">{{ DIRECT_SCOPE }}</p>

          <div class="detail-main__stats">
            <span v-if="canBoost(mainStatus.visibility)"><strong>{{ mainStatus.reblogs_count }}</strong> {{ $t('post.PostDetailPage.s10') }}</span>
            <span><strong>{{ mainStatus.favourites_count }}</strong> {{ $t('post.PostDetailPage.s11') }}</span>
            <span><strong>{{ mainStatus.replies_count }}</strong> {{ $t('post.PostDetailPage.s12') }}</span>
          </div>

          <PostReactions :status="mainStatus" />
          <PostActions
            :status="mainStatus"
            @reply="focusReply"
            @reblog="toggleReblog(mainStatus)"
            @favourite="toggleFavourite(mainStatus)"
            @bookmark="toggleBookmark(mainStatus)"
            @quote="openQuote(mainStatus)"
          />
        </FilteredNotice>
      </article>

      <PostComposer ref="replyComposer" :reply-to="mainStatus" @posted="onReplied" />

      <div v-if="contextLoading" class="page-loading" role="status" :aria-label="$t('post.PostDetailPage.s13')"><t-loading /></div>
      <div v-else-if="contextError" class="detail-context-error" role="alert">
        <span>{{ $t('post.PostDetailPage.s14', [contextError]) }}</span>
        <button type="button" class="btn btn--secondary btn--sm" @click="loadContext">{{ $t('post.PostDetailPage.s3') }}</button>
      </div>

      <!-- Descendants (replies below) -->
      <PostCard
        v-for="s in descendants"
        :key="s.id"
        :status="s"
        @reblog="toggleReblog"
        @favourite="toggleFavourite"
        @bookmark="toggleBookmark"
      />
    </template>
    <ReportDialog v-if="reporting && mainStatus" v-model="reporting" :account-id="mainStatus.account.id" :status-id="mainStatus.id" />
  </div>
</template>

<style scoped>
.detail-page {
  min-height: 100dvh;
}
.detail-context-error {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-4) var(--gutter);
  color: var(--color-label-secondary);
}

/* The focused post steps up one size and gets more air; the surrounding
   thread stays at timeline scale so the hierarchy reads at a glance. */
.detail-main {
  padding: var(--space-4) var(--gutter) 0;
  box-shadow: inset 0 calc(-1 * var(--hairline)) 0 var(--color-separator);
}

.detail-main__header {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  margin-bottom: var(--space-3);
}

.detail-main__avatar {
  flex-shrink: 0;
  border-radius: 50%;
  transition: transform var(--duration-release) var(--ease-out);
}

.detail-main__avatar:active {
  transform: scale(var(--press-scale));
  transition-duration: var(--duration-press);
}

.detail-main__user {
  flex: 1;
  min-width: 0;
}

.detail-main__name {
  display: block;
  font-size: var(--text-headline);
  line-height: var(--leading-headline);
  letter-spacing: var(--tracking-headline);
  font-weight: var(--weight-semibold);
  color: var(--color-label);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.detail-main__name :deep(span) {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.detail-main__username {
  display: block;
  color: var(--color-label-secondary);
  font-size: var(--text-subhead);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.detail-main :deep(.detail-main__content) {
  font-size: var(--text-title-3);
  line-height: 1.45;
  letter-spacing: -0.012em;
  color: var(--color-label);
  margin-bottom: var(--space-4);
}

.detail-main :deep(.detail-main__content:lang(zh)) {
  line-height: var(--leading-cjk);
}

.detail-main__time {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  color: var(--color-label-secondary);
  font-size: var(--text-footnote);
  letter-spacing: var(--tracking-footnote);
  font-variant-numeric: tabular-nums;
  padding-bottom: var(--space-3);
}

.detail-main__visibility {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
}

.detail-main__scope {
  margin: 0 0 var(--space-3);
  color: var(--color-label-secondary);
  font-size: var(--text-footnote);
  line-height: var(--leading-footnote);
}

.detail-main__stats {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-5);
  padding: var(--space-3) 0;
  box-shadow: inset 0 var(--hairline) 0 var(--color-separator);
  color: var(--color-label-secondary);
  font-size: var(--text-subhead);
}

.detail-main__stats strong {
  color: var(--color-label);
  font-weight: var(--weight-semibold);
  font-variant-numeric: tabular-nums;
}

.detail-main :deep(.post-actions) {
  max-width: none;
  margin-top: 0;
  padding: var(--space-0) 0;
  box-shadow: inset 0 var(--hairline) 0 var(--color-separator);
}

.detail-main :deep(.post-actions .action-btn) {
  flex: 1;
  justify-content: center;
}
</style>
