<script setup lang="ts">
import EmojiText from '@/shared/components/common/EmojiText.vue'
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import type { MastodonAccount } from '@/shared/types/mastodon'
import type { FollowState } from '@/domains/user/domain/relationship'
import { followButtonLabel } from '@/domains/user/domain/relationship'
import { isInteractiveClick } from '@/shared/utils/dom'
import UserAvatar from './UserAvatar.vue'
import RichText from './RichText.vue'

const props = defineProps<{
  account: MastodonAccount
  /** Omit to hide the follow button (e.g. for the current user). */
  followState?: FollowState
  followedBy?: boolean
  busy?: boolean
}>()

const emit = defineEmits<{ toggleFollow: [account: MastodonAccount] }>()
const router = useRouter()

const profilePath = computed(() => `/@${props.account.acct}`)

function open(e: MouseEvent | KeyboardEvent) {
  if (e instanceof MouseEvent && isInteractiveClick(e)) return
  router.push(profilePath.value).catch(() => {})
}
</script>

<template>
  <div class="user-item" role="link" tabindex="0" :aria-label="`${account.display_name || account.username} @${account.acct}`"
    @click="open" @keydown.enter.self="open"
  >
    <UserAvatar :account="account" :size="48" />
    <div class="user-item__info">
      <div class="user-item__name"><EmojiText :text="account.display_name || account.username" :emojis="account.emojis" /></div>
      <div class="user-item__username">@{{ account.acct }}</div>
      <RichText v-if="account.note" class="user-item__bio" :html="account.note" :emojis="account.emojis" />
    </div>
    <slot name="action">
      <t-button
        v-if="followState && followState !== 'unknown'"
        theme="default"
        shape="round"
        size="small"
        class="follow-btn"
        :class="{ 'follow-btn--cta': followState === 'none' }"
        :loading="busy"
        @click.stop="emit('toggleFollow', account)"
      >
        {{ followButtonLabel(followState, followedBy) }}
      </t-button>
    </slot>
  </div>
</template>

<style scoped>
/* Row look follows the search page's people rows (style agent's tokens). */
.user-item {
  position: relative;
  display: flex;
  gap: var(--space-3);
  padding: var(--space-3) var(--gutter);
  -webkit-tap-highlight-color: transparent;
  transition: background-color var(--duration-release) var(--ease-out);
  cursor: pointer;
}

.user-item::after {
  content: '';
  position: absolute;
  left: calc(var(--gutter) + 3rem + var(--space-3));
  right: 0;
  bottom: 0;
  height: var(--hairline);
  background: var(--color-separator);
}

@media (hover: hover) and (pointer: fine) {
  .user-item:hover {
    background: var(--color-hover);
  }
}

.user-item:active {
  background: var(--color-pressed);
  transition-duration: 0s;
}

.user-item__info {
  flex: 1;
  min-width: 0;
}

.user-item__name {
  font-size: var(--text-body);
  font-weight: var(--weight-semibold);
  color: var(--color-label);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.user-item__username {
  color: var(--color-label-secondary);
  font-size: var(--text-subhead);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.user-item__bio {
  margin-top: var(--space-1);
  font-size: var(--text-subhead);
  line-height: var(--leading-subhead);
  color: var(--color-label);
  overflow: hidden;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  -webkit-box-orient: vertical;
}

.follow-btn {
  align-self: center;
  flex-shrink: 0;
  font-weight: var(--weight-semibold) !important;
}

.follow-btn--cta {
  background: var(--color-label) !important;
  color: var(--color-bg) !important;
  border: none !important;
}
</style>
