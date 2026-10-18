<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { MessagePlugin } from '@/shared/ui/feedback'
import SegmentedControl from '@/shared/components/common/SegmentedControl.vue'
import ProfileSettingsForm from '../components/ProfileSettingsForm.vue'
import PasswordSettingsForm from '../components/PasswordSettingsForm.vue'
import PasskeySettings from '../components/PasskeySettings.vue'
import TwoFactorSettings from '../components/TwoFactorSettings.vue'
import ConnectionsSettings from '../components/ConnectionsSettings.vue'
import AccountLifecycleSettings from '../components/AccountLifecycleSettings.vue'
import FilterSettings from '../components/FilterSettings.vue'
import DomainBlockSettings from '../components/DomainBlockSettings.vue'
import DiscoverySettings from '@/domains/discovery/presentation/components/DiscoverySettings.vue'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import LanguagePicker from '@/shared/components/common/LanguagePicker.vue'
import { useMediaMode, type MediaMode } from '@/shared/composables/useMediaMode'
import { useTheme, type ThemePreference } from '@/shared/composables/useTheme'

import { t } from '@/shared/i18n'
const router = useRouter()
const route = useRoute()
const sections = [
  { id: 'profile', icon: 'user', label: t('settings.SettingsPage.s11') },
  { id: 'display', icon: 'palette', label: t('settings.SettingsPage.s25') },
  { id: 'discovery', icon: 'earth', label: t('settings.SettingsPage.s4') },
  { id: 'security', icon: 'secured', label: t('settings.SettingsPage.s26') },
  { id: 'privacy', icon: 'browse-off', label: t('settings.SettingsPage.s27') },
  { id: 'connections', icon: 'link', label: t('settings.Connections.title') },
  { id: 'more', icon: 'more', label: t('settings.SettingsPage.s28') },
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
  { value: 'default', label: t('settings.SettingsPage.s29') },
  { value: 'show_all', label: t('settings.SettingsPage.s30') },
  { value: 'hide_all', label: t('settings.SettingsPage.s31') },
]

const appearance = computed<ThemePreference>({
  get: () => preference.value,
  set: (v) => setPreference(v),
})

const appearanceOptions: { value: ThemePreference; label: string; icon: string }[] = [
  { value: 'system', label: t('settings.SettingsPage.s32'), icon: 'desktop' },
  { value: 'light', label: t('settings.SettingsPage.s33'), icon: 'mode-light' },
  { value: 'dark', label: t('settings.SettingsPage.s34'), icon: 'mode-dark' },
]

async function logout() {
  try {
    await authStore.logout()
    router.push({ name: 'landing' }).catch(() => {})
  } catch (e) {
    console.error('Logout failed:', e)
    MessagePlugin.error(t('settings.SettingsPage.s35'))
  }
}
</script>

<template>
  <div class="settings-page">
    <header class="settings-header">
      <h1>{{ $t('settings.SettingsPage.s1') }}</h1>
    </header>
    <div class="settings-layout">
    <nav class="settings-nav" :aria-label="$t('settings.SettingsPage.s3')">
      <router-link v-for="item in sections" :key="item.id" :to="{ path: '/settings', query: { section: item.id } }" :aria-current="activeSection === item.id ? 'page' : undefined"><t-icon :name="item.icon" :size="20" aria-hidden="true" /><span>{{ item.label }}</span></router-link>
    </nav>

    <div class="settings">
      <section v-if="authStore.currentUser && activeSection === 'discovery'" class="settings__section" aria-labelledby="settings-discovery">
        <h2 id="settings-discovery" class="settings__heading">{{ $t('settings.SettingsPage.s4') }}</h2>
        <div class="settings__group settings__group--padded"><DiscoverySettings /></div>
      </section>
      <section class="settings__section" v-show="activeSection === 'display'" aria-labelledby="settings-appearance">
        <h2 id="settings-appearance" class="settings__heading">{{ $t('settings.SettingsPage.s5') }}</h2>
        <div class="settings__group settings__group--padded">
          <SegmentedControl v-model="appearance" :options="appearanceOptions" :label="$t('settings.SettingsPage.s5')" />
        </div>
        <p class="settings__footer">
          {{ $t('settings.SettingsPage.s6') }}
        </p>
      </section>

      <section class="settings__section" v-show="activeSection === 'display'" aria-labelledby="settings-language">
        <h2 id="settings-language" class="settings__heading">{{ $t('shared.LanguagePicker.label') }}</h2>
        <div class="settings__group settings__group--padded">
          <LanguagePicker compact />
        </div>
        <p class="settings__footer">{{ $t('settings.SettingsPage.languageNote') }}</p>
      </section>

      <section class="settings__section" v-show="activeSection === 'display'" aria-labelledby="settings-media">
        <h2 id="settings-media" class="settings__heading">{{ $t('settings.SettingsPage.s7') }}</h2>
        <div class="settings__group settings__group--padded">
          <SegmentedControl v-model="mediaDisplay" :options="mediaOptions" :label="$t('settings.SettingsPage.s7')" />
        </div>
        <p class="settings__footer">{{ $t('settings.SettingsPage.s8') }}</p>
      </section>

      <section v-if="authStore.currentUser" class="settings__section settings__section--profile" v-show="activeSection === 'profile'" aria-labelledby="settings-profile">
        <h2 id="settings-profile" class="settings__heading">{{ $t('settings.SettingsPage.s11') }}</h2>
        <ProfileSettingsForm />
      </section>

      <section v-if="authStore.currentUser" class="settings__section" v-show="activeSection === 'security'" aria-labelledby="settings-password">
        <h2 id="settings-password" class="settings__heading">{{ $t('settings.SettingsPage.s13') }}</h2>
        <div class="settings__group settings__group--padded">
          <PasswordSettingsForm />
        </div>
      </section>

      <section v-if="authStore.currentUser" class="settings__section" v-show="activeSection === 'security'" aria-labelledby="settings-two-factor">
        <h2 id="settings-two-factor" class="settings__heading">{{ $t('settings.TwoFactor.title') }}</h2>
        <div class="settings__group settings__group--padded">
          <TwoFactorSettings />
        </div>
      </section>

      <section v-if="authStore.currentUser" class="settings__section" v-show="activeSection === 'security'" aria-labelledby="settings-passkey">
        <h2 id="settings-passkey" class="settings__heading">{{ $t('settings.SettingsPage.s14') }}</h2>
        <div class="settings__group settings__group--padded">
          <PasskeySettings />
        </div>
      </section>

      <section v-if="authStore.currentUser && activeSection === 'connections'" class="settings__section" aria-labelledby="settings-connections">
        <h2 id="settings-connections" class="settings__heading">{{ $t('settings.Connections.title') }}</h2>
        <ConnectionsSettings />
      </section>

      <section v-if="authStore.currentUser" class="settings__section" v-show="activeSection === 'privacy'" aria-labelledby="settings-filters">
        <h2 id="settings-filters" class="settings__heading">{{ $t('settings.SettingsPage.s16') }}</h2>
        <div class="settings__group settings__group--padded">
          <FilterSettings />
        </div>
      </section>

      <section v-if="authStore.currentUser" class="settings__section" v-show="activeSection === 'privacy'" aria-labelledby="settings-domains">
        <h2 id="settings-domains" class="settings__heading">{{ $t('settings.SettingsPage.s17') }}</h2>
        <div class="settings__group settings__group--padded">
          <DomainBlockSettings />
        </div>
      </section>

      <section v-if="authStore.currentUser" v-show="activeSection === 'more'" class="settings__section"><h2 class="settings__heading">{{ $t('settings.SettingsPage.s18') }}</h2><div class="settings__group"><router-link to="/settings/data" class="settings__row">{{ $t('settings.SettingsPage.s19') }} <t-icon name="chevron-right" :size="16" class="settings__chevron" aria-hidden="true" /></router-link></div></section>

      <AccountLifecycleSettings v-if="authStore.currentUser && activeSection === 'more'" />

      <section v-show="activeSection === 'more'" class="settings__section">
        <div class="settings__group">
          <button type="button" class="settings__row settings__row--danger" @click="logout">
            {{ $t('settings.SettingsPage.s24') }}
          </button>
        </div>
      </section>
    </div>
    </div>
  </div>
</template>

<style src="../settings.css"></style>
