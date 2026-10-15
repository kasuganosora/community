<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import LogoIcon from '@/shared/components/common/LogoIcon.vue'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { visibleAdminNavigation, adminEntry, adminSection } from '../../domain/navigation'
import './admin.css'

const route = useRoute()
const auth = useAuthStore()
const menuOpen = ref(false)
const menuButton = ref<HTMLButtonElement>()
const content = ref<HTMLElement>()
const navigationElement = ref<HTMLElement>()
function revealCurrentSection() {
  if (window.matchMedia('(min-width: 761px)').matches) {
    navigationElement.value?.querySelector('[aria-current="page"]')?.scrollIntoView({ block: 'nearest' })
  }
}
onMounted(revealCurrentSection)
const navigation = computed(() => visibleAdminNavigation(auth.currentUser))
const entry = computed(() => adminEntry(auth.currentUser) || '/settings')
const section = computed(() => adminSection(route.path))
watch(() => route.path, async () => {
  menuOpen.value = false
  await nextTick()
  content.value?.focus({ preventScroll: true })
  revealCurrentSection()
})
function closeMenu() {
  menuOpen.value = false
  menuButton.value?.focus()
}
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
      <nav ref="navigationElement">
        <section v-for="group in navigation" :key="group.label" class="admin-nav-group" :aria-label="group.label">
          <h2>{{ group.label }}</h2>
          <router-link v-for="item in group.items" :key="item.path" :to="item.path" class="admin-nav-link" :class="{ 'admin-nav-link--active': section.path === item.path }" :aria-current="section.path === item.path ? 'page' : undefined">
            <t-icon :name="item.icon" :size="20" aria-hidden="true" /><span :title="item.title">{{ item.title }}</span>
          </router-link>
        </section>
      </nav>
      <div class="admin-sidebar-footer">
        <router-link to="/" class="admin-nav-link"><t-icon name="arrow-left" :size="20" aria-hidden="true" />返回社区</router-link>
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
