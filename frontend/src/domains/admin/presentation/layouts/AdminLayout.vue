<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import LogoIcon from '@/shared/components/common/LogoIcon.vue'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { useTheme } from '@/shared/composables/useTheme'
import { visibleAdminNavigation, adminEntry, adminSection } from '../../domain/navigation'
import './admin.css'

const route = useRoute()
const auth = useAuthStore()
const { preference, setPreference } = useTheme()
const menuOpen = ref(false)
const menuButton = ref<HTMLButtonElement>()
const content = ref<HTMLElement>()
const navigation = computed(() => visibleAdminNavigation(auth.currentUser))
const entry = computed(() => adminEntry(auth.currentUser) || '/settings')
const section = computed(() => adminSection(route.path))
watch(() => route.path, async () => {
  menuOpen.value = false
  await nextTick()
  content.value?.focus({ preventScroll: true })
})
function closeMenu() {
  menuOpen.value = false
  menuButton.value?.focus()
}
function cycleTheme() {
  setPreference(preference.value === 'system' ? 'light' : preference.value === 'light' ? 'dark' : 'system')
}
const themeName = computed(() => ({ system: '跟随系统', light: '浅色', dark: '深色' })[preference.value])
</script>

<template>
  <div class="admin-shell" @keydown.esc="closeMenu">
    <a href="#admin-content" class="admin-skip">跳转到主要内容</a>
    <header class="admin-mobile-header">
      <router-link :to="entry" class="admin-brand"><LogoIcon :size="30" /><strong>Community <small>管理后台</small></strong></router-link>
      <button ref="menuButton" type="button" class="admin-button" :aria-expanded="menuOpen" aria-controls="admin-navigation" @click="menuOpen = !menuOpen">{{ menuOpen ? '收起导航' : '管理导航' }} <t-icon name="menu" aria-hidden="true" /></button>
    </header>
    <aside id="admin-navigation" class="admin-sidebar" :class="{ 'admin-sidebar--open': menuOpen }" aria-label="管理导航">
      <router-link :to="entry" class="admin-brand admin-brand--desktop"><LogoIcon :size="38" /><strong>Community <small>管理后台</small></strong></router-link>
      <nav>
        <section v-for="group in navigation" :key="group.label" class="admin-nav-group" :aria-label="group.label">
          <h2>{{ group.label }}</h2>
          <router-link v-for="item in group.items" :key="item.path" :to="item.path" class="admin-nav-link" :class="{ 'admin-nav-link--active': section.path === item.path }" :aria-current="section.path === item.path ? 'page' : undefined">
            <t-icon :name="item.icon" :size="20" aria-hidden="true" /><span>{{ item.title }}</span>
          </router-link>
        </section>
      </nav>
      <div class="admin-sidebar-footer">
        <router-link to="/" class="admin-nav-link"><t-icon name="arrow-left" :size="20" aria-hidden="true" />返回社区</router-link>
        <button type="button" class="admin-nav-link" @click="cycleTheme"><t-icon name="mode-dark" :size="20" aria-hidden="true" />外观：{{ themeName }}</button>
        <p class="admin-identity"><span class="admin-identity-dot" />{{ auth.currentUser?.is_admin ? '管理员' : '管理成员' }} <bdi>@{{ auth.currentUser?.username }}</bdi></p>
      </div>
    </aside>
    <div class="admin-workspace">
      <div class="admin-topbar"><span>管理后台 <span aria-hidden="true">/</span> <strong>{{ section.title }}</strong></span><router-link to="/" class="admin-back-link">查看社区 <t-icon name="arrow-top-right" aria-hidden="true" /></router-link></div>
      <main id="admin-content" ref="content" tabindex="-1" class="admin-content">
        <router-view />
      </main>
      <footer class="admin-workspace-footer">Community · 连接每一种声音</footer>
    </div>
  </div>
</template>
