<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import LogoIcon from '@/shared/components/common/LogoIcon.vue'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'

const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()

// Use computed to make navItems reactive based on currentUser
const navItems = computed(() => [
  { value: 'home', label: '首页', icon: 'home', route: '/home' },
  { value: 'explore', label: '探索', icon: 'search', route: '/explore' },
  { value: 'notifications', label: '通知', icon: 'notification', route: '/notifications' },
  { value: 'messages', label: '消息', icon: 'mail', route: '/messages' },
  { value: 'bookmarks', label: '收藏', icon: 'bookmark', route: '/bookmarks' },
  { value: 'profile', label: '个人主页', icon: 'user-circle', route: authStore.currentUser ? `/@${authStore.currentUser.username}` : '/login' },
])

const activeItem = computed(() => {
  const path = route.path
  if (path === '/home') return 'home'
  const match = navItems.value.find(item => item.route !== '/home' && path.startsWith(item.route))
  return match?.value || 'home'
})

function navigateTo(item: typeof navItems.value[0]) {
  router.push(item.route)
}

async function handleLogout() {
  await authStore.logout()
  router.push({ name: 'landing' })
}
</script>

<template>
  <div class="left-sidebar">
    <!-- Logo -->
    <button class="sidebar-logo" @click="router.push('/home')" aria-label="返回首页">
      <LogoIcon :size="32" />
    </button>

    <!-- Navigation -->
    <nav class="sidebar-nav" aria-label="主导航">
      <button
        v-for="item in navItems"
        :key="item.value"
        class="nav-item"
        :class="{ 'nav-item--active': activeItem === item.value }"
        @click="navigateTo(item)"
        :aria-current="activeItem === item.value ? 'page' : undefined"
      >
        <t-icon :name="item.icon" :size="28" :aria-hidden="true" />
        <span class="nav-item__label">{{ item.label }}</span>
      </button>
    </nav>

    <!-- Post Button -->
    <div class="sidebar-post">
      <button class="btn btn--primary btn--lg post-btn" disabled>
        发布
      </button>
    </div>

    <!-- User Info -->
    <div class="sidebar-user" v-if="authStore.currentUser">
      <t-dropdown :min-column-width="220">
        <div class="user-info">
          <UserAvatar :account="authStore.currentUser" :size="42" />
          <div class="user-info__text">
            <div class="user-info__name">{{ authStore.currentUser.display_name }}</div>
            <div class="user-info__username">@{{ authStore.currentUser.username }}</div>
          </div>
          <t-icon name="more" :size="18" class="user-info__more" />
        </div>
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
  height: 100%;
  padding: 0 var(--space-3);
}

.sidebar-logo {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 52px;
  height: 52px;
  margin: var(--space-2);
  margin-bottom: var(--space-4);
  padding: 0;
  border-radius: var(--radius-full);
  color: var(--text-primary);
  background: transparent;
  border: none;
  cursor: pointer;
  transition: all var(--transition-fast);
}

.sidebar-logo:hover {
  background-color: var(--bg-hover);
}

.sidebar-logo:active {
  transform: scale(0.95);
}

.sidebar-nav {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.nav-item {
  display: flex;
  align-items: center;
  gap: var(--space-5);
  padding: var(--space-3) var(--space-4);
  border-radius: var(--radius-full);
  color: var(--text-primary);
  font-size: var(--font-xl);
  font-weight: var(--font-normal);
  letter-spacing: var(--tracking-wide);
  background: transparent;
  border: none;
  cursor: pointer;
  transition: all var(--transition-fast);
  text-align: left;
}

.nav-item:hover {
  background-color: var(--bg-hover);
}

.nav-item:active {
  transform: scale(0.98);
}

.nav-item--active {
  font-weight: var(--font-bold);
}

.nav-item__label {
  white-space: nowrap;
}

.nav-item:focus-visible {
  outline: 2px solid var(--accent-blue);
  outline-offset: 2px;
}

.sidebar-post {
  padding: var(--space-4) 0;
}

.post-btn {
  width: 100%;
  min-height: 52px;
  font-size: var(--font-lg);
  letter-spacing: var(--tracking-wide);
}

.sidebar-user {
  padding: var(--space-3) 0;
  margin-top: auto;
}

.user-info {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3);
  border-radius: var(--radius-full);
  cursor: pointer;
  transition: all var(--transition-fast);
}

.user-info:hover {
  background-color: var(--bg-hover);
}

.user-info:active {
  transform: scale(0.98);
}

.user-info__text {
  flex: 1;
  min-width: 0;
}

.user-info__name {
  font-weight: var(--font-bold);
  font-size: var(--font-md);
  color: var(--text-primary);
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

/* Responsive */
@media (max-width: 1280px) {
  .nav-item__label {
    display: none;
  }
  
  .nav-item {
    justify-content: center;
    padding: var(--space-3);
  }
  
  .post-btn {
    width: 52px;
    height: 52px;
    min-height: auto;
    padding: 0;
    border-radius: 50%;
  }
  
  .post-btn::before {
    content: '+';
    font-size: var(--font-2xl);
  }
  
  .user-info__text,
  .user-info__more {
    display: none;
  }
  
  .user-info {
    justify-content: center;
    padding: var(--space-2);
  }
}

@media (max-width: 768px) {
  .left-sidebar {
    padding: 0 var(--space-2);
  }
}
</style>
