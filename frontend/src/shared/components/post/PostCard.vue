<script setup lang="ts">
import { wasEdited } from '@/shared/utils/format'
import PostReactions from '@/shared/components/post/PostReactions.vue'
import EmojiText from '@/shared/components/common/EmojiText.vue'
import { computed, ref } from 'vue'
import { useTimelineRowPin } from '@/shared/composables/useTimelineRow'
import { useRouter } from 'vue-router'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import type { MastodonStatus } from '@/shared/types/mastodon'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { postApi } from '@/domains/post/infrastructure/postApi'
import { visibilityOption, REPLACE_MENU, REPLACE_DIALOG, REPLACE_SUCCESS } from '@/domains/post/domain/compose'
import {
  deletedStatusIds, editedStatuses, markStatusDeleted, markStatusEdited,
} from '@/domains/post/presentation/composables/useStatusActions'
import { isInteractiveClick } from '@/shared/utils/dom'
import { useUiChrome } from '@/shared/composables/useUiChrome'
import ReportDialog from '@/domains/user/presentation/components/ReportDialog.vue'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import RelativeTime from '@/shared/components/common/RelativeTime.vue'
import PostContent from './PostContent.vue'
import PostActions from './PostActions.vue'
import PostComposer from './PostComposer.vue'
import FilteredNotice from './FilteredNotice.vue'

const props = defineProps<{
  status: MastodonStatus
}>()

const emit = defineEmits<{
  reblog: [status: MastodonStatus]
  favourite: [status: MastodonStatus]
  bookmark: [status: MastodonStatus]
}>()

const router = useRouter()
const authStore = useAuthStore()
const editing = ref(false)
const reporting = ref(false)
const menuOpen = ref(false)
useTimelineRowPin(() => editing.value || reporting.value || menuOpen.value, () => {
  editing.value = false; reporting.value = false; menuOpen.value = false
  MessagePlugin.info('请先关闭其他帖子的编辑器或菜单，再打开新的操作。')
})
const { openQuote } = useUiChrome()

const baseStatus = computed(() => props.status.reblog?.id ? props.status.reblog : props.status)
// Pick up edits made elsewhere in this session (e.g. on the detail page).
const displayStatus = computed(() => {
  const edited = editedStatuses.get(baseStatus.value.id)
  return edited ? { ...baseStatus.value, ...edited } : baseStatus.value
})
const isReblog = computed(() => !!props.status.reblog)
const filterMarks = computed(() => {
  const marks = new Set<NonNullable<MastodonStatus['filtered']>[number]>()
  for (const item of [props.status, displayStatus.value]) {
    for (const mark of item.filtered ?? []) marks.add(mark)
  }
  return [...marks]
})
const isDeleted = computed(() => deletedStatusIds.has(props.status.id) || deletedStatusIds.has(displayStatus.value.id))
const isOwn = computed(() => !!authStore.currentUser && displayStatus.value.account.id === authStore.currentUser.id)
const visibility = computed(() => displayStatus.value.visibility !== 'public' ? visibilityOption(displayStatus.value.visibility) : null)
const menuOptions = computed(() => isOwn.value
  ? [
      { content: displayStatus.value.pinned ? '取消置顶' : '置顶', value: 'pin' },
      { content: REPLACE_MENU, value: 'edit' },
      { content: '删除', value: 'delete', theme: 'error' as const },
    ]
  : [{ content: '举报', value: 'report' }])

async function togglePin() {
  try {
    const updated = displayStatus.value.pinned
      ? await postApi.unpin(displayStatus.value.id)
      : await postApi.pin(displayStatus.value.id)
    markStatusEdited(updated)
    MessagePlugin.success(updated.pinned ? '已置顶' : '已取消置顶')
  } catch (e) {
    MessagePlugin.error(e instanceof Error && e.message ? e.message : '置顶失败')
  }
}

function navigateToDetail(query?: Record<string, string>) {
  const s = displayStatus.value
  router.push({
    name: 'post-detail',
    params: { username: s.account.acct || s.account.username, statusId: s.id },
    query,
  }).catch(() => {})
}

function onCardClick(e: MouseEvent) {
  // Let links, buttons, form controls and text selection behave normally.
  if (isInteractiveClick(e)) return
  navigateToDetail()
}

function navigateToProfile(e: Event) {
  e.stopPropagation()
  router.push({
    name: 'profile',
    params: { username: displayStatus.value.account.acct || displayStatus.value.account.username },
  }).catch(() => {})
}

function confirmDelete() {
  const dialog = DialogPlugin.confirm({
    header: '删除帖子？',
    body: '删除后无法恢复。这条帖子的转发和回复也会失去关联。',
    confirmBtn: { content: '删除', theme: 'danger' },
    cancelBtn: '取消',
    onConfirm: async () => {
      try {
        await postApi.deleteStatus(displayStatus.value.id)
        markStatusDeleted(displayStatus.value.id)
        MessagePlugin.success('已删除')
      } catch (e) {
        MessagePlugin.error(e instanceof Error && e.message ? `删除失败：${e.message}` : '删除失败')
      } finally {
        dialog.destroy()
      }
    },
    onClose: () => dialog.destroy(),
  })
}

function onEdited(updated: MastodonStatus) {
  const originalID = displayStatus.value.id
  editing.value = false
  markStatusDeleted(originalID)
  MessagePlugin.success(REPLACE_SUCCESS)
  router.push({ name: 'post-detail', params: { username: updated.account.acct || updated.account.username, statusId: updated.id } }).catch(() => {})
}

function onMenuClick(data: { value?: unknown }) {
  if (data.value === 'edit') editing.value = true
  else if (data.value === 'delete') confirmDelete()
  else if (data.value === 'pin') void togglePin()
  else if (data.value === 'report') reporting.value = true
}
</script>

<template>
  <article
    v-if="!isDeleted"
    class="post-card"
    tabindex="0"
    :aria-labelledby="`post-author-${displayStatus.id}`"
    @click="onCardClick"
    @keydown.enter.self="navigateToDetail()"
  >
    <!-- Reblog header -->
    <div v-if="isReblog" class="post-card__reblog">
      <t-icon name="swap" :size="14" />
      <span><EmojiText :text="status.account.display_name || status.account.username" :emojis="status.account.emojis" /> 转发了</span>
    </div>

    <div class="post-card__body">
      <!-- Avatar -->
      <div
        class="post-card__avatar"
        @click="navigateToProfile"
        role="link"
        tabindex="0"
        :aria-label="`查看 ${displayStatus.account.display_name || displayStatus.account.username} 的个人主页`"
        @keydown.enter="navigateToProfile"
        @keydown.space.prevent="navigateToProfile"
      >
        <UserAvatar :account="displayStatus.account" :size="44" />
      </div>

      <!-- Content -->
      <div class="post-card__content">
        <!-- Header -->
        <div class="post-card__header">
          <span
            class="post-card__name"
            dir="auto"
            @click="navigateToProfile"
            role="link"
            tabindex="0"
            :id="`post-author-${displayStatus.id}`"
            @keydown.enter="navigateToProfile"
            @keydown.space.prevent="navigateToProfile"
          >
            <EmojiText :text="displayStatus.account.display_name || displayStatus.account.username" :emojis="displayStatus.account.emojis" />
          </span>
          <span class="post-card__username">
            @{{ displayStatus.account.acct }}
          </span>
          <span class="post-card__dot">·</span>
          <RelativeTime :datetime="displayStatus.created_at" />
          <span v-if="wasEdited(displayStatus)" class="post-card__edited" title="已编辑">· 已编辑</span>
          <span v-if="visibility" class="post-card__visibility" :title="visibility.label">
            <t-icon :name="visibility.icon" :size="14" />
            <span class="sr-only">{{ visibility.short }}</span>
          </span>
          <t-dropdown
            v-if="authStore.currentUser"
            trigger="click"
            :options="menuOptions"
            :popup-props="{ visible: menuOpen, onVisibleChange: (visible: boolean) => { menuOpen = visible } }"
            @click="onMenuClick"
          >
            <button type="button" class="post-card__menu" aria-label="更多操作" @click.stop>
              <t-icon name="more" :size="16" />
            </button>
          </t-dropdown>
        </div>

        <FilteredNotice :marks="filterMarks">
          <PostContent :status="displayStatus" text-class="post-card__text" />
          <PostReactions :status="displayStatus" />
          <PostActions
            :status="displayStatus"
            @reply="navigateToDetail({ reply: '1' })"
            @reblog="emit('reblog', displayStatus)"
            @favourite="emit('favourite', displayStatus)"
            @bookmark="emit('bookmark', displayStatus)"
            @quote="openQuote(displayStatus)"
          />
        </FilteredNotice>
      </div>
    </div>

    <t-dialog
      v-if="editing"
      :visible="editing"
      :header="REPLACE_DIALOG"
      :footer="false"
      width="600px"
      destroy-on-close
      @close="editing = false"
    >
      <PostComposer :edit-status="displayStatus" autofocus @posted="onEdited" @cancel="editing = false" />
    </t-dialog>
    <ReportDialog v-if="reporting" v-model="reporting" :account-id="displayStatus.account.id" :status-id="displayStatus.id" />
  </article>
</template>

<style scoped>
/* A timeline row: no card chrome, just content separated by an inset
   hairline. Hierarchy comes from weight and tone, not boxes or colour. */
.post-card {
  position: relative;
  padding: var(--space-3) var(--gutter) var(--space-1);
  cursor: pointer;
  -webkit-tap-highlight-color: transparent;
  transition: background-color var(--duration-release) var(--ease-out);
}

/* Inset separator aligned with the text column (iOS list style). */
.post-card::after {
  content: '';
  position: absolute;
  left: calc(var(--gutter) + 2.75rem + var(--space-3));
  right: 0;
  bottom: 0;
  height: var(--hairline);
  background: var(--color-separator);
  pointer-events: none;
}

@media (hover: hover) and (pointer: fine) {
  .post-card:hover {
    background: var(--color-hover);
  }
}

/* Response on pointer-down: instant highlight, eased release. */
.post-card:active {
  background: var(--color-pressed);
  transition-duration: 0s;
}

@media (pointer: coarse) {
  .post-card:active {
    background: transparent;
  }
}

.post-card:has(button:active, a:active, [role='button']:active) {
  background: transparent;
}

.post-card__reblog {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  color: var(--color-label-secondary);
  font-size: var(--text-footnote);
  line-height: var(--leading-footnote);
  letter-spacing: var(--tracking-footnote);
  font-weight: var(--weight-medium);
  padding-left: calc(2.75rem + var(--space-3) - 1.25rem);
  margin-bottom: var(--space-1);
}

.post-card__reblog :deep(svg) {
  width: 1rem;
  flex-shrink: 0;
  color: var(--color-repost);
}

.post-card__body {
  display: flex;
  gap: var(--space-3);
}

.post-card__avatar {
  flex-shrink: 0;
  align-self: flex-start;
  border-radius: 50%;
  transition: transform var(--duration-release) var(--ease-out);
}

.post-card__avatar:active {
  transform: scale(var(--press-scale));
  transition-duration: var(--duration-press);
}

.post-card__content {
  flex: 1;
  min-width: 0;
}

.post-card__header {
  display: flex;
  align-items: baseline;
  gap: var(--space-1);
  min-width: 0;
  font-size: var(--text-subhead);
  line-height: var(--leading-subhead);
}

.post-card__name {
  flex: 0 1 auto;
  max-width: 65%;
  font-size: var(--text-body);
  font-weight: var(--weight-semibold);
  letter-spacing: var(--tracking-body);
  color: var(--color-label);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  unicode-bidi: plaintext;
}

.post-card__menu {
  position: relative;
  display: grid;
  place-items: center;
  align-self: center;
  width: 1.75rem;
  height: 1.75rem;
  margin: -0.25rem calc(-1 * var(--space-1)) -0.25rem auto;
  border-radius: var(--radius-full);
  color: var(--color-label-secondary);
  flex-shrink: 0;
  transition:
    background-color var(--duration-fast) var(--ease-out),
    transform var(--duration-release) var(--ease-out);
}

/* 44pt hit area around the small glyph. */
.post-card__menu::after {
  content: '';
  position: absolute;
  inset: -0.5rem;
}

@media (hover: hover) and (pointer: fine) {
  .post-card__menu:hover {
    background: var(--color-hover);
    color: var(--color-label);
  }
}

.post-card__menu:active {
  background: var(--color-pressed);
  transform: scale(var(--press-scale-small));
  transition-duration: var(--duration-press);
}

.post-card__edited {
  flex-shrink: 0;
  color: var(--color-label-secondary);
  white-space: nowrap;
}

.post-card__visibility {
  flex-shrink: 0;
  display: inline-flex;
  align-self: center;
  color: var(--color-label-secondary);
}

@media (hover: hover) and (pointer: fine) {
  .post-card__name:hover {
    text-decoration: underline;
    text-decoration-thickness: 1px;
    text-underline-offset: 2px;
  }
}

.post-card__username {
  flex: 0 1 auto;
  min-width: 0;
  direction: ltr;
  unicode-bidi: isolate;
  color: var(--color-label-secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.post-card__dot {
  flex-shrink: 0;
  color: var(--color-label-tertiary);
}

.post-card__header :deep(time),
.post-card__header :deep(.relative-time) {
  flex-shrink: 0;
  color: var(--color-label-secondary);
  font-variant-numeric: tabular-nums;
}

.post-card__content :deep(.post-card__text) {
  margin-top: var(--space-0);
  font-size: var(--text-body);
  line-height: var(--leading-body);
  letter-spacing: var(--tracking-body);
  color: var(--color-label);
  overflow-wrap: break-word;
}

.post-card__content :deep(.post-card__text:lang(zh)),
.post-card__content :deep(.post-card__text p:lang(zh)) {
  line-height: var(--leading-cjk);
}

.post-card__content :deep(.post-card__text p + p) {
  margin-top: var(--space-2);
}
</style>
