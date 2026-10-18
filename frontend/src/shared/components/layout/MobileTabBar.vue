<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { useUiChrome } from '@/shared/composables/useUiChrome'
import { activeNavSection } from '@/shared/utils/nav'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'

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
const { openAccount } = useUiChrome()

// Specific labels for what's behind each tab (HIG: name tabs for content).
const tabs = [
  { value: 'home', label: t('shared.MobileTabBar.s4'), icon: 'home', iconActive: 'home-filled', to: '/' },
  { value: 'explore', label: t('shared.MobileTabBar.s5'), icon: 'search', iconActive: 'search', to: '/explore' },
  { value: 'notifications', label: t('shared.MobileTabBar.s6'), icon: 'notification', iconActive: 'notification-filled', to: '/notifications' },
  { value: 'messages', label: t('shared.MobileTabBar.s7'), icon: 'mail', iconActive: 'mail-filled', to: '/messages' },
]

const active = computed(() => {
  const section = activeNavSection(route.path, authStore.currentUser?.username)
  return section === 'profile' ? 'me' : section
})

function go(to: string) {
  if (route.path === to) {
    // Re-tapping the current tab scrolls to top, like a native tab bar.
    window.scrollTo({ top: 0, behavior: 'smooth' })
    return
  }
  router.push(to).catch(() => {})
}
</script>

<template>
  <nav class="tabbar" :aria-label="$t('shared.MobileTabBar.s1')">
    <button
      v-for="tab in tabs"
      :key="tab.value"
      type="button"
      class="tabbar__item"
      :class="{ 'tabbar__item--active': active === tab.value }"
      :aria-current="active === tab.value ? 'page' : undefined"
      :aria-label="navLabel(tab.value, tab.label)"
      @click="go(tab.to)"
    >
      <span class="tabbar__icon">
        <t-icon :name="active === tab.value ? tab.iconActive : tab.icon" :size="26" />
        <span v-if="tab.value === 'notifications' && unreadNotifications > 0" class="unread-badge" data-testid="notification-badge" aria-hidden="true">{{ unreadLabel }}</span>
          <span v-if="hasDot(tab.value)" class="unread-dot" :data-testid="`unread-dot-${tab.value}`" aria-hidden="true" />
      </span>
      <span class="tabbar__label">{{ tab.label }}</span>
    </button>
    <button
      v-if="authStore.currentUser"
      type="button"
      class="tabbar__item"
      :class="{ 'tabbar__item--active': active === 'me' }"
      aria-haspopup="dialog"
      :aria-label="$t('shared.MobileTabBar.s2')"
      @click="openAccount"
    >
      <span class="tabbar__avatar">
        <UserAvatar :account="authStore.currentUser" :size="26" />
      </span>
      <span class="tabbar__label">{{ $t('shared.MobileTabBar.s3') }}</span>
    </button>
  </nav>
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

/* A floating translucent layer: the timeline scrolls underneath and shows
   through. The bright hairline on top reads as light catching the edge. */
.tabbar {
  position: fixed;
  left: 0;
  right: 0;
  bottom: 0;
  z-index: var(--z-bar);
  display: flex;
  align-items: stretch;
  height: calc(var(--tabbar-height) + var(--safe-bottom));
  padding: 0 var(--space-2) var(--safe-bottom);
  background: var(--material-bar);
  -webkit-backdrop-filter: var(--material-blur-bar);
  backdrop-filter: var(--material-blur-bar);
}

/* Scroll edge: a short fade above the bar, only while content still passes
   underneath (html[data-scroll-more]). No hard divider. */
.tabbar::before {
  content: '';
  position: absolute;
  left: 0;
  right: 0;
  bottom: 100%;
  height: 1.25rem;
  pointer-events: none;
  background: linear-gradient(to top, var(--scroll-edge-fade), transparent);
  opacity: 0;
  transition: opacity var(--duration-fast) var(--ease-out);
}

:global(html[data-scroll-more]) .tabbar::before {
  opacity: 1;
}

.tabbar__item {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 1px;
  min-width: var(--hit-target);
  min-height: var(--hit-target);
  /* Vibrant label: full-weight ink on the blurred bar, not flat gray. */
  color: var(--color-label-vibrant);
  transition: color var(--duration-fast) var(--ease-out);
  -webkit-touch-callout: none;
  -webkit-user-select: none;
  user-select: none;
  touch-action: manipulation;
}

.tabbar__item > :first-child {
  transition: transform var(--duration-release) var(--ease-spring);
}

/* Press feedback on touch-down: the glyph dips, the label stays put. */
.tabbar__item:active > :first-child {
  transform: scale(var(--press-scale-small));
  transition-duration: var(--duration-press);
  transition-timing-function: var(--ease-out);
}

.tabbar__item--active {
  color: var(--color-accent-text);
}

.tabbar__label {
  font-size: var(--text-caption-2);
  letter-spacing: 0.012em;
  font-weight: var(--weight-semibold);
  line-height: 1.2;
}

.tabbar__avatar {
  display: grid;
  place-items: center;
  border-radius: var(--radius-full);
  box-shadow: 0 0 0 1.5px transparent;
}

.tabbar__item--active .tabbar__avatar {
  box-shadow: 0 0 0 1.5px var(--color-accent-text);
}
</style>
