<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useSettingsScope } from '../composables/useSettingsScope'
import { MessagePlugin } from '@/shared/ui/feedback'
import { authApi, type AuthorizedApp } from '@/domains/auth/infrastructure/authApi'

import { t } from '@/shared/i18n'
const apps = ref<AuthorizedApp[]>([])
const loadError = ref('')
const fetching = ref(true)
const revoking = ref('')
const scope = useSettingsScope(() => { apps.value = []; loadError.value = ''; fetching.value = false; revoking.value = '' })

async function refresh() {
  const current = scope()
  fetching.value = true
  loadError.value = ''
  try {
    const result = await authApi.listAuthorizedApps()
    if (current()) apps.value = result
  } catch {
    if (current()) loadError.value = t('settings.AuthorizedApps.s6')
  } finally {
    if (current()) fetching.value = false
  }
}

async function revoke(id: string) {
  if (revoking.value || fetching.value) return
  const current = scope()
  revoking.value = id
  try {
    await authApi.revokeAuthorizedApp(id)
    if (!current()) return
    apps.value = apps.value.filter(app => app.id !== id)
  } catch {
    if (current()) MessagePlugin.error(t('settings.AuthorizedApps.s7'))
  } finally {
    if (current()) revoking.value = ''
  }
}

onMounted(() => {
  refresh().catch(() => {})
})
</script>

<template>
  <div class="apps">
    <p v-if="fetching" class="hint" role="status">{{ $t('settings.AuthorizedApps.s1') }}</p>
    <div v-else-if="loadError" class="settings-error" role="alert">{{ loadError }} <button type="button" class="btn btn--ghost" @click="refresh">{{ $t('settings.AuthorizedApps.s2') }}</button></div>
    <p v-if="!fetching && !loadError && apps.length === 0" class="hint">{{ $t('settings.AuthorizedApps.s3') }}</p>
    <ul v-else-if="!fetching && !loadError && apps.length">
      <li v-for="app in apps" :key="app.id">
        <span>
          <strong>{{ app.name }}</strong>
          <small v-if="app.scopes">{{ app.scopes }}</small>
        </span>
        <button type="button" :disabled="!!revoking" :aria-busy="revoking === app.id" @click="revoke(app.id)">{{ revoking === app.id ? $t('settings.AuthorizedApps.s4') : $t('settings.AuthorizedApps.s5') }}</button>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.apps { display: flex; flex-direction: column; gap: var(--space-2); }
.hint { color: var(--color-label-secondary); font-size: var(--text-footnote); margin: 0; }
ul { list-style: none; margin: 0; padding: 0; }
li { display: flex; justify-content: space-between; gap: var(--space-3); align-items: center; min-height: var(--hit-target); }
small { display: block; color: var(--color-label-secondary); }
button { color: var(--color-danger); background: none; border: 0; cursor: pointer; }
</style>
