<script setup lang="ts">
import { adminEntry } from '@/domains/admin/domain/navigation'
import EmojiText from '@/shared/components/common/EmojiText.vue'
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { MessagePlugin } from 'tdesign-vue-next'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import SegmentedControl from '@/shared/components/common/SegmentedControl.vue'
import AvatarDecorationPicker from '../components/AvatarDecorationPicker.vue'
import ProfileSettingsForm from '../components/ProfileSettingsForm.vue'
import PasswordSettingsForm from '../components/PasswordSettingsForm.vue'
import PasskeySettings from '../components/PasskeySettings.vue'
import AuthorizedApps from '../components/AuthorizedApps.vue'
import FilterSettings from '../components/FilterSettings.vue'
import DomainBlockSettings from '../components/DomainBlockSettings.vue'
import DiscoverySettings from '@/domains/discovery/presentation/components/DiscoverySettings.vue'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { useMediaMode, type MediaMode } from '@/shared/composables/useMediaMode'
import { useTheme, type ThemePreference } from '@/shared/composables/useTheme'

const router = useRouter()
const route = useRoute()
const sections = [
  { id: 'profile', label: '个人资料' },
  { id: 'display', label: '显示偏好' },
  { id: 'discovery', label: '内容发现' },
  { id: 'security', label: '账号安全' },
  { id: 'privacy', label: '内容过滤' },
  { id: 'more', label: '更多' },
]
const activeSection = computed(() => sections.some(item => item.id === route.query.section) ? route.query.section : 'profile')
const authStore = useAuthStore()
const { preference, setPreference } = useTheme()
const { mode: mediaMode, setMode: setMediaMode } = useMediaMode()
const mediaDisplay = computed<MediaMode>({
  get: () => mediaMode.value,
  set: (v) => { void setMediaMode(v) },
})
const mediaOptions: { value: MediaMode; label: string }[] = [
  { value: 'default', label: '默认' },
  { value: 'show_all', label: '总是显示' },
  { value: 'hide_all', label: '总是隐藏' },
]

const appearance = computed<ThemePreference>({
  get: () => preference.value,
  set: (v) => setPreference(v),
})

const appearanceOptions: { value: ThemePreference; label: string; icon: string }[] = [
  { value: 'system', label: '自动', icon: 'desktop' },
  { value: 'light', label: '浅色', icon: 'mode-light' },
  { value: 'dark', label: '深色', icon: 'mode-dark' },
]

function openProfile() {
  const u = authStore.currentUser
  if (u) router.push(`/@${u.username}`).catch(() => {})
}

async function logout() {
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
  <div class="settings-page">
    <header class="settings-header">
      <h1>设置</h1>
      <p>管理你的个人资料、账号与浏览偏好。</p>
    </header>
    <nav class="settings-nav" aria-label="设置分类">
      <router-link v-for="item in sections" :key="item.id" :to="{ path: '/settings', query: { section: item.id } }" :aria-current="activeSection === item.id ? 'page' : undefined">{{ item.label }}</router-link>
    </nav>

    <div class="settings">
      <section v-if="authStore.currentUser && activeSection === 'discovery'" class="settings__section" aria-labelledby="settings-discovery">
        <h2 id="settings-discovery" class="settings__heading">内容发现</h2>
        <div class="settings__group settings__group--padded"><DiscoverySettings /></div>
      </section>
      <section class="settings__section" v-show="activeSection === 'display'" aria-labelledby="settings-appearance">
        <h2 id="settings-appearance" class="settings__heading">外观</h2>
        <div class="settings__group settings__group--padded">
          <SegmentedControl v-model="appearance" :options="appearanceOptions" label="外观" />
        </div>
        <p class="settings__footer">
          “自动”跟随设备外观。动态效果与透明度也会遵循系统偏好。
        </p>
      </section>

      <section class="settings__section" v-show="activeSection === 'display'" aria-labelledby="settings-media">
        <h2 id="settings-media" class="settings__heading">媒体显示</h2>
        <div class="settings__group settings__group--padded">
          <SegmentedControl v-model="mediaDisplay" :options="mediaOptions" label="媒体显示" />
        </div>
        <p class="settings__footer">“默认”按帖子的敏感标记隐藏，点击后才加载原图。</p>
      </section>

      <section v-if="authStore.currentUser" class="settings__section" v-show="activeSection === 'profile'" aria-labelledby="settings-account">
        <h2 id="settings-account" class="settings__heading">账户</h2>
        <div class="settings__group">
          <button type="button" class="settings__row" @click="openProfile">
            <UserAvatar :account="authStore.currentUser" :size="40" />
            <span class="settings__row-text">
              <span class="settings__row-title"><EmojiText :text="authStore.currentUser.display_name || authStore.currentUser.username" :emojis="authStore.currentUser.emojis" /></span>
              <span class="settings__row-sub">@{{ authStore.currentUser.username }} · 查看个人主页</span>
            </span>
            <t-icon name="chevron-right" :size="16" class="settings__chevron" />
          </button>
        </div>
      </section>

      <section v-if="authStore.currentUser" class="settings__section" v-show="activeSection === 'profile'" aria-labelledby="settings-profile">
        <h2 id="settings-profile" class="settings__heading">个人资料</h2>
        <div class="settings__group settings__group--padded">
          <ProfileSettingsForm />
</div>
      </section>

      <section v-if="authStore.currentUser" v-show="activeSection === 'profile'" class="settings__section" aria-labelledby="settings-decoration">
        <h2 id="settings-decoration" class="settings__heading">头像挂件</h2>
        <div class="settings__group settings__group--padded"><AvatarDecorationPicker /></div>
      </section>
      <section v-if="authStore.currentUser" class="settings__section" v-show="activeSection === 'security'" aria-labelledby="settings-password">
        <h2 id="settings-password" class="settings__heading">修改密码</h2>
        <div class="settings__group settings__group--padded">
          <PasswordSettingsForm />
        </div>
      </section>

      <section v-if="authStore.currentUser" class="settings__section" v-show="activeSection === 'security'" aria-labelledby="settings-passkey">
        <h2 id="settings-passkey" class="settings__heading">通行密钥</h2>
        <div class="settings__group settings__group--padded">
          <PasskeySettings />
        </div>
      </section>

      <section v-if="authStore.currentUser" class="settings__section" v-show="activeSection === 'security'" aria-labelledby="settings-apps">
        <h2 id="settings-apps" class="settings__heading">已授权的应用</h2>
        <div class="settings__group settings__group--padded">
          <AuthorizedApps />
        </div>
      </section>

      <section v-if="authStore.currentUser" class="settings__section" v-show="activeSection === 'privacy'" aria-labelledby="settings-filters">
        <h2 id="settings-filters" class="settings__heading">过滤器</h2>
        <div class="settings__group settings__group--padded">
          <FilterSettings />
        </div>
      </section>

      <section v-if="authStore.currentUser" class="settings__section" v-show="activeSection === 'privacy'" aria-labelledby="settings-domains">
        <h2 id="settings-domains" class="settings__heading">域名屏蔽</h2>
        <div class="settings__group settings__group--padded">
          <DomainBlockSettings />
        </div>
      </section>

      <section v-if="authStore.currentUser" v-show="activeSection === 'more'" class="settings__section"><h2 class="settings__heading">数据</h2><div class="settings__group"><router-link to="/settings/data" class="settings__row">个人数据导入与导出 <t-icon name="chevron-right" :size="16" class="settings__chevron" aria-hidden="true" /></router-link></div></section>

      <section v-show="activeSection === 'more'" class="settings__section"><h2 class="settings__heading">社区</h2><div class="settings__group"><router-link to="/announcements" class="settings__row">社区公告 <t-icon name="chevron-right" :size="16" class="settings__chevron" aria-hidden="true" /></router-link></div></section>
      <section v-if="adminEntry(authStore.currentUser)" class="settings__section" v-show="activeSection === 'more'" aria-labelledby="settings-admin">
        <h2 id="settings-admin" class="settings__heading">管理</h2>
        <div class="settings__group">
          <router-link :to="adminEntry(authStore.currentUser)!" class="settings__row">管理后台 <t-icon name="chevron-right" :size="16" class="settings__chevron" aria-hidden="true" /></router-link>
        </div>
      </section>

      <section v-show="activeSection === 'more'" class="settings__section">
        <div class="settings__group">
          <button type="button" class="settings__row settings__row--danger" @click="logout">
            退出登录
          </button>
        </div>
      </section>
    </div>
  </div>
</template>

<style src="../settings.css"></style>
