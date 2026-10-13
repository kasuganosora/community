<script setup lang="ts">
import EmojiText from '@/shared/components/common/EmojiText.vue'
import { useRouter } from 'vue-router'
import { MessagePlugin } from 'tdesign-vue-next'
import BottomSheet from './BottomSheet.vue'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { useUiChrome } from '@/shared/composables/useUiChrome'

const router = useRouter()
const authStore = useAuthStore()
const { accountOpen } = useUiChrome()

function go(to: string) {
  accountOpen.value = false
  router.push(to).catch(() => {})
}

async function logout() {
  accountOpen.value = false
  try {
    await authStore.logout()
    router.push({ name: 'landing' }).catch(() => {})
  } catch (e) {
    console.error('Logout failed:', e)
    MessagePlugin.error('退出失败，请稍后重试')
  }
}
</script>

<template>
  <BottomSheet v-model="accountOpen" label="账户">
    <div v-if="authStore.currentUser" class="account">
      <button type="button" class="account__me" @click="go(`/@${authStore.currentUser.username}`)">
        <UserAvatar :account="authStore.currentUser" :size="52" />
        <span class="account__text">
          <span class="account__name"><EmojiText :text="authStore.currentUser.display_name || authStore.currentUser.username" :emojis="authStore.currentUser.emojis" /></span>
          <span class="account__handle">@{{ authStore.currentUser.username }}</span>
        </span>
        <t-icon name="chevron-right" :size="18" class="account__chevron" />
      </button>

      <!-- Inset grouped list, iOS Settings style -->
      <div class="group">
        <button type="button" class="group__row" @click="go('/messages')">
          <span class="group__icon" style="--tint: #5856d6"><t-icon name="mail" :size="18" /></span>
          <span class="group__label">消息</span>
          <t-icon name="chevron-right" :size="16" class="group__chevron" />
        </button>
        <button type="button" class="group__row" @click="go('/messages/groups')">
          <span class="group__icon" style="--tint: #007aff"><t-icon name="usergroup" :size="18" /></span>
          <span class="group__label">群组</span>
          <t-icon name="chevron-right" :size="16" class="group__chevron" />
        </button>
        <button type="button" class="group__row" @click="go('/bookmarks')">
          <span class="group__icon" style="--tint: #ff9500"><t-icon name="bookmark-filled" :size="18" /></span>
          <span class="group__label">收藏</span>
          <t-icon name="chevron-right" :size="16" class="group__chevron" />
        </button>
        <button type="button" class="group__row" @click="go('/lists')">
          <span class="group__icon" style="--tint: #34c759"><t-icon name="view-list" :size="18" /></span>
          <span class="group__label">列表</span>
          <t-icon name="chevron-right" :size="16" class="group__chevron" />
        </button>
        <button type="button" class="group__row" @click="go('/settings')">
          <span class="group__icon" style="--tint: #8e8e93"><t-icon name="setting-filled" :size="18" /></span>
          <span class="group__label">设置</span>
          <t-icon name="chevron-right" :size="16" class="group__chevron" />
        </button>
      </div>

      <div class="group">
        <button type="button" class="group__row group__row--danger" @click="logout">
          <span class="group__label">退出 @{{ authStore.currentUser.username }}</span>
        </button>
      </div>
    </div>
  </BottomSheet>
</template>

<style scoped>
.account {
  display: flex;
  flex-direction: column;
  gap: var(--space-5);
  padding-top: var(--space-1);
}

.account__me,
.group__row {
  transition: background-color var(--duration-fast) var(--ease-out);
}

.account__me {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  width: 100%;
  padding: var(--space-3);
  border-radius: var(--radius-lg);
  background: var(--color-bg-row, var(--color-bg-elevated));
  text-align: left;
}

.account__me:active,
.group__row:active {
  background: var(--color-pressed);
}

.account__text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.account__name {
  font-size: var(--text-headline);
  letter-spacing: var(--tracking-headline);
  font-weight: var(--weight-semibold);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.account__handle {
  font-size: var(--text-footnote);
  color: var(--color-label-secondary);
}

.account__chevron,
.group__chevron {
  color: var(--color-label-tertiary);
  flex-shrink: 0;
}

.group {
  border-radius: var(--radius-lg);
  background: var(--color-bg-row, var(--color-bg-elevated));
  overflow: hidden;
}

.group__row {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  width: 100%;
  min-height: var(--hit-target);
  padding: 0 var(--space-4);
  font-size: var(--text-body);
  text-align: left;
  color: var(--color-label);
}

.group__row + .group__row {
  box-shadow: inset 3.25rem var(--hairline) 0 var(--color-bg-row, var(--color-bg-elevated)), inset 0 var(--hairline) 0 var(--color-separator);
}

.group__icon {
  display: grid;
  place-items: center;
  width: 1.75rem;
  height: 1.75rem;
  border-radius: 7px;
  background: var(--tint);
  color: #fff;
  flex-shrink: 0;
}

.group__label {
  flex: 1;
}

.group__row--danger {
  justify-content: center;
  color: var(--color-danger);
  font-weight: var(--weight-medium);
}

.group__row--danger .group__label {
  flex: none;
}
</style>
