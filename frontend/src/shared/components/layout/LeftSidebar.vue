<script setup lang="ts">
import { adminEntry } from '@/domains/admin/domain/navigation'
import EmojiText from '@/shared/components/common/EmojiText.vue'
import { computed, ref } from 'vue'
import AccountSwitchList from '@/shared/components/account/AccountSwitchList.vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { MessagePlugin } from '@/shared/ui/feedback'
import LogoIcon from '@/shared/components/common/LogoIcon.vue'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import { useUiChrome } from '@/shared/composables/useUiChrome'
import { activeNavSection } from '@/shared/utils/nav'

import { t } from '@/shared/i18n'
import { useNotificationUnread } from '@/shared/composables/useNotificationUnread'
import { useNavUnread } from '@/shared/composables/useNavUnread'
const { unread: unreadNotifications, label: unreadLabel } = useNotificationUnread()
const { announcements: unreadAnnouncements, conversations: unreadConversations } = useNavUnread()
/** Announcements and messages show a plain dot; notifications keep their count badge. */
function hasDot(value: string) {
  return (value === 'announcements' && unreadAnnouncements.value > 0) || (value === 'messages' && unreadConversations.value > 0)
}
function navLabel(value: string, label: string) {
  if (value === 'notifications' && unreadNotifications.value > 0) return t('shared.unread.navCount', [label, unreadLabel.value])
  return hasDot(value) ? t('shared.unread.navDot', [label]) : label
}
const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const { openCompose } = useUiChrome()
const menuOpen = ref(false)

function goHome() {
  router.push({ name: 'home' }).catch(() => {})
}

// Use computed to make navItems reactive based on currentUser
const navItems = computed(() => [
  { value: 'home', label: t('shared.LeftSidebar.s8'), icon: 'home', iconActive: 'home-filled', route: '/' },
  { value: 'explore', label: t('shared.LeftSidebar.s9'), icon: 'search', iconActive: 'search', route: '/explore' },
  { value: 'notifications', label: t('shared.LeftSidebar.s10'), icon: 'notification', iconActive: 'notification-filled', route: '/notifications' },
  { value: 'announcements', label: t('shared.LeftSidebar.s11'), icon: 'loudspeaker', iconActive: 'loudspeaker-filled', route: '/announcements' },
  { value: 'messages', label: t('shared.LeftSidebar.s12'), icon: 'mail', iconActive: 'mail-filled', route: '/messages' },
  { value: 'groups', label: t('shared.LeftSidebar.s13'), icon: 'usergroup', iconActive: 'usergroup', route: '/groups' },
  { value: 'bookmarks', label: t('shared.LeftSidebar.s14'), icon: 'bookmark', iconActive: 'bookmark-filled', route: '/bookmarks' },
  { value: 'lists', label: t('shared.LeftSidebar.s15'), icon: 'view-list', iconActive: 'view-list', route: '/lists' },
  { value: 'profile', label: t('shared.LeftSidebar.s16'), icon: 'user-circle', iconActive: 'user-circle-filled', route: authStore.currentUser ? `/@${authStore.currentUser.username}` : '/login' },
  ...(adminEntry(authStore.currentUser) ? [{ value: 'admin', label: t('shared.LeftSidebar.s17'), icon: 'dashboard', iconActive: 'dashboard', route: adminEntry(authStore.currentUser)! }] : []),
  { value: 'settings', label: t('shared.LeftSidebar.s18'), icon: 'setting', iconActive: 'setting-filled', route: '/settings' },
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
    if (await authStore.logout()) return // switched to another signed-in account
    router.push({ name: 'landing' }).catch(() => {})
  } catch (e) {
    console.error('Logout failed:', e)
    MessagePlugin.error(t('shared.LeftSidebar.s19'))
  }
}
</script>

<template>
  <div class="left-sidebar">
    <!-- Logo -->
    <button class="sidebar-logo" @click="goHome" :aria-label="$t('shared.LeftSidebar.s1')">
      <LogoIcon :size="32" />
    </button>

    <!-- Navigation -->
    <nav class="sidebar-nav" :aria-label="$t('shared.LeftSidebar.s2')">
      <button
        v-for="item in navItems"
        :key="item.value"
        class="nav-item"
        :class="{ 'nav-item--active': activeItem === item.value }"
        :aria-label="navLabel(item.value, item.label)"
        :title="item.label"
        @click="navigateTo(item)"
        :aria-current="activeItem === item.value ? 'page' : undefined"
      >
        <span class="nav-item__icon">
          <t-icon :name="activeItem === item.value ? item.iconActive : item.icon" :size="26" :aria-hidden="true" />
          <span v-if="item.value === 'notifications' && unreadNotifications > 0" class="unread-badge" data-testid="notification-badge" aria-hidden="true">{{ unreadLabel }}</span>
          <span v-if="hasDot(item.value)" class="unread-dot" :data-testid="`unread-dot-${item.value}`" aria-hidden="true" />
        </span>
        <span class="nav-item__label" :title="item.label">{{ item.label }}</span>
      </button>
    </nav>

    <!-- Post Button -->
    <div class="sidebar-post">
      <button
        class="btn btn--primary btn--lg post-btn"
        data-shot="compose"
        @click="handleCompose"
        :aria-label="authStore.currentUser ? $t('shared.LeftSidebar.s3') : $t('shared.LeftSidebar.s4')"
      >
        <t-icon name="edit" :size="20" class="post-btn__icon" />
        <span class="post-btn__label">{{ $t('shared.LeftSidebar.s5') }}</span>
      </button>
    </div>

    <!-- User Info -->
    <div class="sidebar-user" v-if="authStore.currentUser">
      <t-popup v-model:visible="menuOpen" trigger="click" placement="top-left" :overlay-inner-style="{ padding: '4px' }">
        <button type="button" class="user-info" data-qa="sidebar-account" aria-haspopup="menu" :aria-expanded="menuOpen" :aria-label="$t('shared.LeftSidebar.s6', [authStore.currentUser.username])">
          <UserAvatar :account="authStore.currentUser" :size="42" />
          <div class="user-info__text">
            <div class="user-info__name"><EmojiText :text="authStore.currentUser.display_name || authStore.currentUser.username" :emojis="authStore.currentUser.emojis" /></div>
            <div class="user-info__username">@{{ authStore.currentUser.username }}</div>
          </div>
          <t-icon name="more" :size="18" class="user-info__more" />
        </button>
        <template #content>
          <AccountSwitchList @close="menuOpen = false" />
          <div class="user-menu__divider" />
          <button type="button" class="user-menu__logout" data-qa="acct-logout" @click="menuOpen = false; handleLogout()">
            {{ $t('shared.LeftSidebar.s7', [authStore.currentUser.username]) }}
          </button>
        </template>
      </t-popup>
    </div>
  </div>
</template>

<style scoped>
.nav-item__icon,
.tabbar__icon {
  position: relative;
  display: inline-flex;
}

.unread-dot {
  position: absolute;
  top: -1px;
  right: -2px;
  width: 9px;
  height: 9px;
  border-radius: var(--radius-full);
  background: var(--color-danger, #f4212e);
  box-shadow: 0 0 0 2px var(--color-bg);
}

.unread-badge {
  position: absolute;
  top: -6px;
  right: -10px;
  min-width: 1.125rem;
  height: 1.125rem;
  padding: 0 4px;
  border-radius: var(--radius-full);
  background: var(--color-accent);
  color: #fff;
  font-size: 0.6875rem;
  font-weight: var(--weight-semibold);
  line-height: 1.125rem;
  text-align: center;
  font-variant-numeric: tabular-nums;
  box-shadow: 0 0 0 2px var(--color-bg);
}

.user-menu__divider { height: 1px; margin: 4px 12px; background: var(--color-separator); }
.user-menu__logout { width: 100%; min-height: 40px; padding: 0 12px; text-align: left; color: var(--color-danger); border-radius: 8px; }
.user-menu__logout:hover { background: var(--color-pressed); }

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
  /* Long ru/de labels must not push past the 275px nav. */
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.nav-item { min-width: 0; max-width: 100%; }
.nav-item > :first-child { flex-shrink: 0; }

.post-btn__label {
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
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
@media (max-width: 79.99rem) {
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
