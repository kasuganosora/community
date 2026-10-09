<script setup lang="ts">
import { adminEntry } from '@/domains/admin/domain/navigation'
import EmojiText from '@/shared/components/common/EmojiText.vue'
import { computed } from 'vue'
import { useRouter } from 'vue-router'
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
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { useMediaMode, type MediaMode } from '@/shared/composables/useMediaMode'
import { useTheme, type ThemePreference } from '@/shared/composables/useTheme'

const router = useRouter()
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
    <div class="page-header">
      <h1 class="page-header__title">设置</h1>
    </div>

    <div class="settings">
      <section class="settings__section" aria-labelledby="settings-appearance">
        <h2 id="settings-appearance" class="settings__heading">外观</h2>
        <div class="settings__group settings__group--padded">
          <SegmentedControl v-model="appearance" :options="appearanceOptions" label="外观" />
        </div>
        <p class="settings__footer">
          “自动”会跟随设备的浅色 / 深色外观切换。动效和半透明材质同时遵循系统的“减弱动态效果”与“降低透明度”设置。
        </p>
      </section>

      <section class="settings__section" aria-labelledby="settings-media">
        <h2 id="settings-media" class="settings__heading">媒体显示</h2>
        <div class="settings__group settings__group--padded">
          <SegmentedControl v-model="mediaDisplay" :options="mediaOptions" label="媒体显示" />
        </div>
        <p class="settings__footer">“默认”按帖子的敏感标记隐藏，点击后才加载原图。</p>
      </section>

      <section v-if="authStore.currentUser" class="settings__section" aria-labelledby="settings-account">
        <h2 id="settings-account" class="settings__heading">账户</h2>
        <div class="settings__group">
          <button type="button" class="settings__row" @click="openProfile">
            <UserAvatar :account="authStore.currentUser" :size="40" />
            <span class="settings__row-text">
              <span class="settings__row-title"><EmojiText :text="authStore.currentUser.display_name || authStore.currentUser.username" :emojis="authStore.currentUser.emojis" /></span>
              <span class="settings__row-sub">@{{ authStore.currentUser.username }}</span>
            </span>
            <t-icon name="chevron-right" :size="16" class="settings__chevron" />
          </button>
        </div>
      </section>

      <section v-if="authStore.currentUser" class="settings__section" aria-labelledby="settings-profile">
        <h2 id="settings-profile" class="settings__heading">个人资料</h2>
        <div class="settings__group settings__group--padded">
          <ProfileSettingsForm />
          <AvatarDecorationPicker />
        </div>
      </section>

      <section v-if="authStore.currentUser" class="settings__section" aria-labelledby="settings-password">
        <h2 id="settings-password" class="settings__heading">修改密码</h2>
        <div class="settings__group settings__group--padded">
          <PasswordSettingsForm />
        </div>
      </section>

      <section v-if="authStore.currentUser" class="settings__section" aria-labelledby="settings-passkey">
        <h2 id="settings-passkey" class="settings__heading">通行密钥</h2>
        <div class="settings__group settings__group--padded">
          <PasskeySettings />
        </div>
      </section>

      <section v-if="authStore.currentUser" class="settings__section" aria-labelledby="settings-apps">
        <h2 id="settings-apps" class="settings__heading">已授权的应用</h2>
        <div class="settings__group settings__group--padded">
          <AuthorizedApps />
        </div>
      </section>

      <section v-if="authStore.currentUser" class="settings__section" aria-labelledby="settings-filters">
        <h2 id="settings-filters" class="settings__heading">过滤器</h2>
        <div class="settings__group settings__group--padded">
          <FilterSettings />
        </div>
      </section>

      <section v-if="authStore.currentUser" class="settings__section" aria-labelledby="settings-domains">
        <h2 id="settings-domains" class="settings__heading">域名屏蔽</h2>
        <div class="settings__group settings__group--padded">
          <DomainBlockSettings />
        </div>
      </section>

      <section v-if="authStore.currentUser" class="settings__section"><h2 class="settings__heading">数据</h2><div class="settings__group"><router-link to="/settings/data" class="settings__row">个人数据导入与导出</router-link></div></section>

      <section class="settings__section"><h2 class="settings__heading">社区</h2><div class="settings__group"><router-link to="/announcements" class="settings__row">社区公告 <t-icon name="chevron-right" :size="16" /></router-link></div></section>
      <section v-if="adminEntry(authStore.currentUser)" class="settings__section" aria-labelledby="settings-admin">
        <h2 id="settings-admin" class="settings__heading">管理</h2>
        <div class="settings__group">
          <router-link :to="adminEntry(authStore.currentUser)!" class="settings__row">管理后台 <t-icon name="chevron-right" :size="16" /></router-link>
        </div>
      </section>

      <section class="settings__section">
        <div class="settings__group">
          <button type="button" class="settings__row settings__row--danger" @click="logout">
            退出登录
          </button>
        </div>
      </section>
    </div>
  </div>
</template>

<style scoped>
.settings-page {
  min-height: 100dvh;
}

/* Inset grouped layout (iOS Settings): small secondary headings, rounded
   groups on the grouped background, explanatory footers. */
.settings {
  display: flex;
  flex-direction: column;
  gap: var(--space-6);
  padding: var(--space-4) var(--gutter) var(--space-12);
}

.settings__heading {
  padding: 0 var(--space-4) var(--space-2);
  font-size: var(--text-footnote);
  letter-spacing: var(--tracking-footnote);
  font-weight: var(--weight-regular);
  color: var(--color-label-secondary);
}

.settings__group {
  border-radius: var(--radius-md);
  background: var(--color-bg-grouped);
  overflow: hidden;
}

.settings__group--padded {
  padding: var(--space-3);
}

.settings__footer {
  padding: var(--space-2) var(--space-4) 0;
  font-size: var(--text-footnote);
  line-height: var(--leading-footnote);
  color: var(--color-label-secondary);
}

.settings__row {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  width: 100%;
  min-height: var(--hit-target);
  padding: var(--space-2) var(--space-4);
  text-align: left;
  color: var(--color-label);
  font-size: var(--text-body);
  -webkit-tap-highlight-color: transparent;
  transition: background-color var(--duration-release) var(--ease-out);
}

@media (hover: hover) and (pointer: fine) {
  .settings__row:hover {
    background: var(--color-hover);
  }
}

.settings__row:active {
  background: var(--color-pressed);
  transition-duration: 0s;
}

@media (pointer: coarse) {
  .settings__row:active {
    background: transparent;
  }
}

.settings__row-text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.settings__row-title {
  font-weight: var(--weight-semibold);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.settings__row-sub {
  font-size: var(--text-footnote);
  color: var(--color-label-secondary);
}

.settings__chevron {
  flex-shrink: 0;
  color: var(--color-label-tertiary);
}

.settings__row--danger {
  justify-content: center;
  color: var(--color-danger);
  font-weight: var(--weight-medium);
}
</style>
