<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import type { MastodonStatus } from '@/shared/types/mastodon'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { postApi } from '@/domains/post/infrastructure/postApi'
import {
  deletedStatusIds, editedStatuses, markStatusDeleted, markStatusEdited,
} from '@/domains/post/presentation/composables/useStatusActions'
import { isInteractiveClick } from '@/shared/utils/dom'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import RelativeTime from '@/shared/components/common/RelativeTime.vue'
import PostContent from './PostContent.vue'
import PostActions from './PostActions.vue'
import PostComposer from './PostComposer.vue'

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

const baseStatus = computed(() => props.status.reblog?.id ? props.status.reblog : props.status)
// Pick up edits made elsewhere in this session (e.g. on the detail page).
const displayStatus = computed(() => {
  const edited = editedStatuses.get(baseStatus.value.id)
  return edited ? { ...baseStatus.value, ...edited } : baseStatus.value
})
const isReblog = computed(() => !!props.status.reblog)
const isDeleted = computed(() => deletedStatusIds.has(props.status.id) || deletedStatusIds.has(displayStatus.value.id))
const isOwn = computed(() => !!authStore.currentUser && displayStatus.value.account.id === authStore.currentUser.id)

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
    body: '删除后无法恢复，转发和回复也会失去关联。',
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
  markStatusEdited(updated)
  editing.value = false
  MessagePlugin.success('已保存')
}

function onMenuClick(data: { value?: unknown }) {
  if (data.value === 'edit') editing.value = true
  else if (data.value === 'delete') confirmDelete()
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
      <span>{{ status.account.display_name || status.account.username }} 转发了</span>
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
            @click="navigateToProfile"
            role="link"
            tabindex="0"
            :id="`post-author-${displayStatus.id}`"
            @keydown.enter="navigateToProfile"
            @keydown.space.prevent="navigateToProfile"
          >
            {{ displayStatus.account.display_name || displayStatus.account.username }}
          </span>
          <span class="post-card__username">
            @{{ displayStatus.account.acct }}
          </span>
          <span class="post-card__dot">·</span>
          <RelativeTime :datetime="displayStatus.created_at" />
          <span v-if="displayStatus.edited_at" class="post-card__edited" title="已编辑">· 已编辑</span>
          <t-dropdown
            v-if="isOwn"
            trigger="click"
            :options="[{ content: '编辑', value: 'edit' }, { content: '删除', value: 'delete', theme: 'error' }]"
            @click="onMenuClick"
          >
            <button type="button" class="post-card__menu" aria-label="更多操作" @click.stop>
              <t-icon name="more" :size="16" />
            </button>
          </t-dropdown>
        </div>

        <PostContent :status="displayStatus" text-class="post-card__text" />

        <!-- Actions -->
        <PostActions
          :status="displayStatus"
          @reply="navigateToDetail({ reply: '1' })"
          @reblog="emit('reblog', displayStatus)"
          @favourite="emit('favourite', displayStatus)"
          @bookmark="emit('bookmark', displayStatus)"
        />
      </div>
    </div>

    <t-dialog
      v-if="editing"
      :visible="editing"
      header="编辑帖子"
      :footer="false"
      width="600px"
      destroy-on-close
      @close="editing = false"
    >
      <PostComposer :edit-status="displayStatus" autofocus @posted="onEdited" @cancel="editing = false" />
    </t-dialog>
  </article>
</template>

<style scoped>
.post-card {
  padding: var(--space-4);
  border-bottom: 1px solid var(--border-color);
  cursor: pointer;
  transition: all var(--transition-fast);
}

.post-card:hover {
  background: var(--bg-hover);
}

.post-card__reblog {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  color: var(--text-secondary);
  font-size: var(--font-sm);
  font-weight: var(--font-bold);
  padding-left: 56px;
  margin-bottom: var(--space-1);
  letter-spacing: var(--tracking-wide);
}

.post-card__body {
  display: flex;
  gap: var(--space-3);
}

.post-card__avatar {
  flex-shrink: 0;
}

.post-card__content {
  flex: 1;
  min-width: 0;
}

.post-card__header {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  flex-wrap: nowrap;
  overflow: hidden;
  margin-bottom: var(--space-1);
}

.post-card__name {
  font-weight: var(--font-bold);
  font-size: var(--font-md);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  color: var(--text-primary);
  letter-spacing: var(--tracking-tight);
}

.post-card__menu {
  margin-left: auto;
  padding: var(--space-1);
  border-radius: var(--radius-full);
  color: var(--text-secondary);
  flex-shrink: 0;
}

.post-card__menu:hover {
  background: var(--accent-blue-light, rgba(29, 155, 240, 0.1));
  color: var(--accent-blue);
}

.post-card__edited {
  color: var(--text-secondary);
  font-size: var(--font-sm);
  white-space: nowrap;
}

.post-card__name:hover {
  text-decoration: underline;
}

.post-card__username {
  color: var(--text-secondary);
  font-size: var(--font-sm);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  flex-shrink: 1;
}

.post-card__dot {
  color: var(--text-secondary);
  flex-shrink: 0;
  font-weight: var(--font-bold);
}

.post-card__text {
  margin-top: var(--space-1);
  font-size: var(--font-md);
  color: var(--text-primary);
  line-height: var(--leading-relaxed);
  letter-spacing: var(--tracking-normal);
}

/* Chinese text optimizations */
.post-card__text :deep(p) {
  line-height: var(--leading-relaxed);
  margin-bottom: var(--space-3);
}

.post-card__text :deep(a) {
  color: var(--accent-blue);
  text-decoration: none;
  border-bottom: 1px solid transparent;
  transition: border-color var(--transition-fast);
}

.post-card__text :deep(a:hover) {
  border-bottom-color: var(--accent-blue);
}
</style>
