<script setup lang="ts">
import { adminEntry } from '@/domains/admin/domain/navigation'
import EmojiText from '@/shared/components/common/EmojiText.vue'
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { MessagePlugin } from 'tdesign-vue-next'
import LogoIcon from '@/shared/components/common/LogoIcon.vue'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import { useUiChrome } from '@/shared/composables/useUiChrome'
import { activeNavSection } from '@/shared/utils/nav'

const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const { openCompose } = useUiChrome()

function goHome() {
  router.push({ name: 'home' }).catch(() => {})
}

// Use computed to make navItems reactive based on currentUser
const navItems = computed(() => [
  { value: 'home', label: '首页', icon: 'home', iconActive: 'home-filled', route: '/' },
  { value: 'explore', label: '探索', icon: 'search', iconActive: 'search', route: '/explore' },
  { value: 'notifications', label: '通知', icon: 'notification', iconActive: 'notification-filled', route: '/notifications' },
  { value: 'announcements', label: '公告', icon: 'notification', iconActive: 'notification-filled', route: '/announcements' },
  { value: 'messages', label: '消息', icon: 'mail', iconActive: 'mail-filled', route: '/messages' },
  { value: 'groups', label: '群组', icon: 'usergroup', iconActive: 'usergroup', route: '/groups' },
  { value: 'bookmarks', label: '收藏', icon: 'bookmark', iconActive: 'bookmark-filled', route: '/bookmarks' },
  { value: 'lists', label: '列表', icon: 'view-list', iconActive: 'view-list', route: '/lists' },
  { value: 'profile', label: '个人主页', icon: 'user-circle', iconActive: 'user-circle-filled', route: authStore.currentUser ? `/@${authStore.currentUser.username}` : '/login' },
  ...(adminEntry(authStore.currentUser) ? [{ value: 'admin', label: '管理后台', icon: 'dashboard', iconActive: 'dashboard', route: adminEntry(authStore.currentUser)! }] : []),
  { value: 'settings', label: '设置', icon: 'setting', iconActive: 'setting-filled', route: '/settings' },
])

// Segment-aware: a status page under /@me/status/… used to light up 个人主页.
const activeItem = computed(() => activeNavSection(route.path, authStore.currentUser?.username))

function navigateTo(item: typeof navItems.value[0]) {
  // Catch to avoid unhandled rejection if navigation is cancelled/redirected
  router.push(item.route).catch(() => {})
}

function handleCompose() {
  if (authStore.currentUser) {
    openCompose()
  } else {
    router.push({ name: 'login' }).catch(() => {})
  }
}

async function handleLogout() {
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
  <div class="left-sidebar">
    <!-- Logo -->
    <button class="sidebar-logo" @click="goHome" aria-label="返回首页">
      <LogoIcon :size="32" />
    </button>

    <!-- Navigation -->
    <nav class="sidebar-nav" aria-label="主导航">
      <button
        v-for="item in navItems"
        :key="item.value"
        class="nav-item"
        :class="{ 'nav-item--active': activeItem === item.value }"
        :aria-label="item.label"
        :title="item.label"
        @click="navigateTo(item)"
        :aria-current="activeItem === item.value ? 'page' : undefined"
      >
        <t-icon :name="activeItem === item.value ? item.iconActive : item.icon" :size="26" :aria-hidden="true" />
        <span class="nav-item__label">{{ item.label }}</span>
      </button>
    </nav>

    <!-- Post Button -->
    <div class="sidebar-post">
      <button
        class="btn btn--primary btn--lg post-btn"
        data-shot="compose"
        @click="handleCompose"
        :aria-label="authStore.currentUser ? '发布新帖子' : '请先登录'"
      >
        <t-icon name="edit" :size="20" class="post-btn__icon" />
        <span class="post-btn__label">发布</span>
      </button>
    </div>

    <!-- User Info -->
    <div class="sidebar-user" v-if="authStore.currentUser">
      <t-dropdown :min-column-width="220">
        <button type="button" class="user-info" aria-haspopup="menu" :aria-label="`账户菜单：@${authStore.currentUser.username}`">
          <UserAvatar :account="authStore.currentUser" :size="42" />
          <div class="user-info__text">
            <div class="user-info__name"><EmojiText :text="authStore.currentUser.display_name || authStore.currentUser.username" :emojis="authStore.currentUser.emojis" /></div>
            <div class="user-info__username">@{{ authStore.currentUser.username }}</div>
          </div>
          <t-icon name="more" :size="18" class="user-info__more" />
        </button>
        <template #dropdown>
          <t-dropdown-menu>
            <t-dropdown-item @click="handleLogout">
              退出 @{{ authStore.currentUser.username }}
            </t-dropdown-item>
          </t-dropdown-menu>
        </template>
      </t-dropdown>
    </div>
  </div>
</template>

<style scoped>
.left-sidebar {
  display: flex;
  flex-direction: column;
  min-height: 100%;
  padding: var(--space-2) var(--space-3) var(--space-3);
}

.sidebar-logo {
  display: grid;
  place-items: center;
  width: 3rem;
  height: 3rem;
  margin: var(--space-1) 0 var(--space-3) var(--space-1);
  border-radius: var(--radius-full);
  color: var(--color-label);
  touch-action: manipulation;
  -webkit-user-select: none;
  user-select: none;
  -webkit-touch-callout: none;
  transition:
    background-color var(--duration-fast) var(--ease-out),
    transform var(--duration-release) var(--ease-spring);
}

.sidebar-logo:active {
  background-color: var(--color-pressed);
  transform: scale(var(--press-scale-small));
  transition-duration: var(--duration-press);
  transition-timing-function: var(--ease-out);
}

.sidebar-nav {
  display: flex;
  flex-direction: column;
  gap: var(--space-0);
}

/* Sidebar rows: quiet by default; the selected row gets a filled glyph,
   heavier weight and a soft fill — colour is reserved for actions. */
.nav-item {
  display: flex;
  align-items: center;
  gap: var(--space-4);
  min-height: 3rem;
  padding: 0 var(--space-4) 0 var(--space-3);
  border-radius: var(--radius-md);
  color: var(--color-label);
  font-size: var(--text-headline);
  letter-spacing: var(--tracking-headline);
  font-weight: var(--weight-regular);
  text-align: left;
  touch-action: manipulation;
  -webkit-user-select: none;
  user-select: none;
  -webkit-touch-callout: none;
  transition:
    background-color var(--duration-fast) var(--ease-out),
    transform var(--duration-release) var(--ease-spring);
}

.nav-item:active {
  background-color: var(--color-pressed);
  transform: scale(0.98);
  transition-duration: var(--duration-press);
  transition-timing-function: var(--ease-out);
}

@media (hover: hover) and (pointer: fine) {
  .nav-item:hover { background-color: var(--color-hover); }
  .sidebar-logo:hover { background-color: var(--color-hover); }
  .user-info:hover { background-color: var(--color-hover); }
}

.nav-item--active {
  font-weight: var(--weight-semibold);
  background-color: var(--color-fill);
}

@media (hover: hover) and (pointer: fine) {
  .nav-item--active:hover { background-color: var(--color-fill); }
}

.nav-item__label {
  white-space: nowrap;
}

.sidebar-post {
  padding: var(--space-5) 0 var(--space-4);
}

.post-btn {
  width: 100%;
  min-height: 3rem;
  box-shadow: var(--shadow-fab);
}

.sidebar-user {
  margin-top: auto;
  padding-top: var(--space-3);
}

.user-info {
  width: 100%;
  text-align: left;
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-2);
  border-radius: var(--radius-lg);
  cursor: pointer;
  transition:
    background-color var(--duration-fast) var(--ease-out),
    transform var(--duration-release) var(--ease-spring);
}

.user-info:active {
  background-color: var(--color-pressed);
  transform: scale(0.98);
  transition-duration: var(--duration-press);
  transition-timing-function: var(--ease-out);
}

.user-info__text {
  flex: 1;
  min-width: 0;
}

.user-info__name {
  font-weight: var(--weight-semibold);
  font-size: var(--text-subhead);
  color: var(--color-label);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.user-info__username {
  color: var(--color-label-secondary);
  font-size: var(--text-footnote);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.user-info__more {
  color: var(--color-label-secondary);
}

/* ---- Collapsed rail (tablet / narrow desktop) ---- */
@media (max-width: 74.99rem) {
  .left-sidebar {
    align-items: center;
    padding-left: var(--space-2);
    padding-right: var(--space-2);
  }

  .sidebar-logo {
    margin-left: 0;
  }

  .nav-item__label,
  .post-btn__label,
  .user-info__text,
  .user-info__more {
    display: none;
  }

  .nav-item {
    justify-content: center;
    width: 3rem;
    padding: 0;
    border-radius: var(--radius-full);
  }

  .post-btn {
    width: 3rem;
    min-height: 3rem;
    padding: 0;
  }

  .user-info {
    padding: var(--space-1);
    border-radius: var(--radius-full);
  }
}
</style>
