<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import LogoIcon from '@/shared/components/common/LogoIcon.vue'

const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()

const navItems = [
  { value: 'home', label: 'Home', icon: 'home', route: '/home' },
  { value: 'explore', label: 'Explore', icon: 'search', route: '/explore' },
  { value: 'notifications', label: 'Notifications', icon: 'notification', route: '/notifications' },
  { value: 'messages', label: 'Messages', icon: 'mail', route: '/messages' },
  { value: 'bookmarks', label: 'Bookmarks', icon: 'bookmark', route: '/bookmarks' },
  { value: 'profile', label: 'Profile', icon: 'user-circle', route: `/@${authStore.currentUser?.username || 'me'}` },
]

const activeItem = computed(() => {
  const path = route.path
  if (path === '/home') return 'home'
  const match = navItems.find(item => item.route !== '/home' && path.startsWith(item.route))
  return match?.value || 'home'
})

function navigateTo(item: typeof navItems[0]) {
  router.push(item.route)
}

function handleLogout() {
  authStore.logout()
  router.push({ name: 'landing' })
}
</script>

<template>
  <div class="left-sidebar">
    <!-- Logo -->
    <div class="sidebar-logo" @click="router.push('/home')">
      <LogoIcon :size="28" />
    </div>

    <!-- Navigation -->
    <nav class="sidebar-nav">
      <div
        v-for="item in navItems"
        :key="item.value"
        class="nav-item"
        :class="{ 'nav-item--active': activeItem === item.value }"
        @click="navigateTo(item)"
      >
        <t-icon :name="item.icon" :size="26" />
        <span class="nav-item__label">{{ item.label }}</span>
      </div>
    </nav>

    <!-- Post Button -->
    <div class="sidebar-post">
      <t-button theme="primary" shape="round" block size="large" class="post-btn">
        Post
      </t-button>
    </div>

    <!-- User Info -->
    <div class="sidebar-user" v-if="authStore.currentUser">
      <t-dropdown :min-column-width="200">
        <div class="user-info">
          <t-avatar :size="40" :image="authStore.currentUser.avatar || undefined">
            {{ authStore.currentUser.display_name?.charAt(0) || 'U' }}
          </t-avatar>
          <div class="user-info__text">
            <div class="user-info__name">{{ authStore.currentUser.display_name }}</div>
            <div class="user-info__username">@{{ authStore.currentUser.username }}</div>
          </div>
          <t-icon name="more" :size="18" class="user-info__more" />
        </div>
        <template #dropdown>
          <t-dropdown-menu>
            <t-dropdown-item @click="handleLogout">
              Log out @{{ authStore.currentUser.username }}
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
  height: 100%;
  padding: 0 12px;
}

.sidebar-logo {
  padding: 12px;
  margin-bottom: 4px;
  cursor: pointer;
  width: fit-content;
  border-radius: var(--radius-full);
  transition: background-color var(--transition-fast);
  color: var(--text-primary);
}

.sidebar-logo:hover {
  background-color: var(--bg-hover);
}

.sidebar-nav {
  flex: 1;
}

.nav-item {
  display: flex;
  align-items: center;
  gap: 20px;
  padding: 12px;
  border-radius: var(--radius-full);
  cursor: pointer;
  transition: background-color var(--transition-fast);
  color: var(--text-primary);
  font-size: var(--font-xl);
  width: fit-content;
  user-select: none;
}

.nav-item:hover {
  background-color: var(--bg-hover);
}

.nav-item--active {
  font-weight: 700;
}

.nav-item__label {
  margin-right: 16px;
}

@media (max-width: 1024px) {
  .nav-item__label {
    display: none;
  }
  .nav-item {
    justify-content: center;
  }
  .post-btn :deep(.t-button__text) {
    display: none;
  }
  .post-btn {
    width: 52px !important;
    height: 52px !important;
    padding: 0 !important;
    border-radius: 50% !important;
  }
  .user-info__text,
  .user-info__more {
    display: none;
  }
}

.sidebar-post {
  padding: 16px 0;
}

.post-btn {
  font-size: var(--font-lg) !important;
  font-weight: 700 !important;
  padding: 14px 0 !important;
}

.sidebar-user {
  padding: 12px 0;
  margin-top: auto;
}

.user-info {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px;
  border-radius: var(--radius-full);
  cursor: pointer;
  transition: background-color var(--transition-fast);
}

.user-info:hover {
  background-color: var(--bg-hover);
}

.user-info__text {
  flex: 1;
  min-width: 0;
}

.user-info__name {
  font-weight: 700;
  font-size: var(--font-md);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.user-info__username {
  color: var(--text-secondary);
  font-size: var(--font-sm);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.user-info__more {
  color: var(--text-secondary);
}
</style>
