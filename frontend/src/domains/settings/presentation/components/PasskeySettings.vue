<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useSettingsScope } from '../composables/useSettingsScope'
import { MessagePlugin } from '@/shared/ui/feedback'
import { authApi, type PasskeyItem } from '@/domains/auth/infrastructure/authApi'
import { createPasskey, passkeySupported } from '@/shared/utils/webauthn'

import { t } from '@/shared/i18n'
const supported = passkeySupported()
const items = ref<PasskeyItem[]>([])
const loadError = ref('')
const fetching = ref(true)
const loading = ref(false)
const name = ref('')
const removing = ref('')
const scope = useSettingsScope(() => { items.value = []; name.value = ''; loadError.value = ''; fetching.value = false; loading.value = false; removing.value = '' })

async function refresh() {
  const current = scope()
  fetching.value = true
  loadError.value = ''
  try {
    const result = await authApi.listPasskeys()
    if (current()) items.value = result
  } catch {
    if (current()) loadError.value = t('settings.PasskeySettings.s11')
  } finally {
    if (current()) fetching.value = false
  }
}

async function add() {
  if (loading.value || removing.value || fetching.value) return
  const current = scope()
  loading.value = true
  try {
    const options = await authApi.passkeyRegisterBegin(name.value.trim() || 'Passkey')
    if (!current()) return
    const credential = await createPasskey(options)
    if (!current()) return
    await authApi.passkeyRegisterFinish(credential)
    if (!current()) return
    name.value = ''
    await refresh()
    if (current()) MessagePlugin.success(t('settings.PasskeySettings.s12'))
  } catch (e) {
    if (current()) MessagePlugin.error(e instanceof Error ? e.message : t('settings.PasskeySettings.s13'))
  } finally {
    if (current()) loading.value = false
  }
}

async function remove(id: string) {
  if (removing.value || loading.value || fetching.value) return
  const current = scope()
  removing.value = id
  try {
    await authApi.deletePasskey(id)
    if (current()) items.value = items.value.filter(item => item.id !== id)
  } catch {
    if (current()) MessagePlugin.error(t('settings.PasskeySettings.s14'))
  } finally {
    if (current()) removing.value = ''
  }
}

onMounted(() => {
  refresh().catch(() => {})
})
</script>

<template>
  <div class="passkeys">
    <p v-if="fetching" class="hint" role="status">{{ $t('settings.PasskeySettings.s1') }}</p>
    <div v-else-if="loadError" class="settings-error" role="alert">{{ loadError }} <button type="button" class="btn btn--ghost" @click="refresh">{{ $t('settings.PasskeySettings.s2') }}</button></div>
    <p v-if="!supported" class="hint">{{ $t('settings.PasskeySettings.s3') }}</p>
    <template v-else>
      <form class="settings-form" @submit.prevent="add">
        <div class="settings-field">
          <label for="passkey-name" class="settings-field__label">{{ $t('settings.PasskeySettings.s4') }}</label>
          <input id="passkey-name" v-model="name" class="settings-input" :placeholder="$t('settings.PasskeySettings.s5')" autocomplete="off" />
        </div>
        <div class="settings-actions"><button type="submit" class="btn btn--primary" :disabled="loading || !!removing || fetching">{{ loading ? $t('settings.PasskeySettings.s6') : $t('settings.PasskeySettings.s7') }}</button></div>
      </form>
      <p v-if="!fetching && !loadError && items.length === 0" class="hint">{{ $t('settings.PasskeySettings.s8') }}</p>
      <ul v-else-if="!fetching && !loadError && items.length">
        <li v-for="item in items" :key="item.id">
          <span>{{ item.name }}</span>
          <button type="button" class="passkey-remove" :disabled="!!removing || loading" :aria-busy="removing === item.id" @click="remove(item.id)">{{ removing === item.id ? $t('settings.PasskeySettings.s9') : $t('settings.PasskeySettings.s10') }}</button>
        </li>
      </ul>
    </template>
  </div>
</template>

<style scoped>
.passkeys { display: flex; flex-direction: column; gap: var(--space-3); }
.add { display: flex; gap: var(--space-2); flex-wrap: wrap; }
.add :deep(.t-input) { flex: 1; min-width: 10rem; }
.hint { color: var(--color-label-secondary); font-size: var(--text-footnote); margin: 0; }
ul { list-style: none; margin: 0; padding: 0; }
li { display: flex; justify-content: space-between; align-items: center; min-height: var(--hit-target); }
.passkey-remove { color: var(--color-danger); background: none; border: 0; cursor: pointer; }
</style>
