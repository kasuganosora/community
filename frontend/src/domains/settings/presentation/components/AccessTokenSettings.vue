<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { connectionsApi, type AccessToken } from '../../infrastructure/connectionsApi'
import { useSettingsScope } from '../composables/useSettingsScope'
import { t } from '@/shared/i18n'
const rows = ref<AccessToken[]>([])
const loading = ref(true), busy = ref(false), error = ref(''), name = ref(''), scopes = ref(['read']), expires = ref(30), secret = ref(''), creating = ref(false), copied = ref(false)
const scope = useSettingsScope(() => { loading.value = false; rows.value = []; secret.value = ''; name.value = ''; error.value = ''; busy.value = false; creating.value = false })
async function load() {
  const current = scope(); loading.value = true; error.value = ''
  try { const result = await connectionsApi.tokens(); if (current()) rows.value = result }
  catch { if (current()) error.value = t('settings.Connections.loadError') }
  finally { if (current()) loading.value = false }
}
async function create() {
  if (busy.value) return
  const current = scope(); busy.value = true; error.value = ''; secret.value = ''; copied.value = false
  try {
    const result = await connectionsApi.createToken({ name: name.value.trim(), scopes: scopes.value, expires_in_days: expires.value })
    if (!current()) return
    rows.value.unshift(result.item); secret.value = result.token; name.value = ''; creating.value = false
  } catch { if (current()) error.value = t('settings.Connections.saveError') }
  finally { if (current()) busy.value = false }
}
async function revoke(id: string) {
  if (busy.value || !window.confirm(t('settings.Connections.revokeConfirm'))) return
  const current = scope(); busy.value = true; error.value = ''
  try { await connectionsApi.revokeToken(id); if (current()) { rows.value = rows.value.filter(row => row.id !== id); secret.value = '' } }
  catch { if (current()) error.value = t('settings.Connections.saveError') }
  finally { if (current()) busy.value = false }
}
async function copy() { try { await navigator.clipboard.writeText(secret.value); copied.value = true } catch { error.value = t('settings.Connections.copyError') } }
onMounted(load)
</script>
<template>
  <section class="connection-panel">
    <div class="connection-body" style="padding-top: 20px">
      <div class="connection-row"><h2 class="connection-section-title">{{ $t('settings.Connections.tokens') }}</h2><button v-if="!creating" class="btn btn--primary" :disabled="loading || busy" @click="creating = true">{{ $t('settings.Connections.generate') }}</button></div>
      <p v-if="error" class="connection-error" role="alert">{{ error }} <button v-if="!busy" class="btn btn--ghost" @click="load">{{ $t('settings.Connections.retry') }}</button></p>
      <div v-if="secret" class="connection-secret" role="status"><strong>{{ $t('settings.Connections.saveSecret') }}</strong><textarea :value="secret" readonly :aria-label="$t('settings.Connections.token')" spellcheck="false" /><div class="connection-actions"><button class="btn btn--primary" @click="copy">{{ $t(copied ? 'settings.Connections.copied' : 'settings.Connections.copy') }}</button><button class="btn btn--ghost" @click="secret = ''">{{ $t('settings.Connections.close') }}</button></div></div>
      <form v-if="creating" class="connection-form" @submit.prevent="create">
        <label>{{ $t('settings.Connections.name') }}<input v-model="name" required maxlength="100" autocomplete="off" :disabled="busy" /></label>
        <fieldset :disabled="busy"><legend>{{ $t('settings.Connections.permissions') }}</legend><label v-for="key in ['read', 'write']" :key="key" class="connection-toggle"><input v-model="scopes" type="checkbox" :value="key" role="switch" /><span>{{ $t(`settings.Connections.${key}`) }}</span></label></fieldset>
        <label>{{ $t('settings.Connections.expiry') }}<select v-model="expires" :disabled="busy"><option v-for="days in [7, 30, 90, 365]" :key="days" :value="days">{{ $t('settings.Connections.days', { count: days }) }}</option><option :value="0">{{ $t('settings.Connections.never') }}</option></select></label>
        <div class="connection-actions"><button class="btn btn--primary" :disabled="busy || !name.trim() || !scopes.length" :aria-busy="busy">{{ $t(busy ? 'settings.Connections.saving' : 'settings.Connections.generate') }}</button><button type="button" class="btn btn--ghost" :disabled="busy" @click="creating = false">{{ $t('settings.Connections.cancel') }}</button></div>
      </form>
      <p v-if="loading" class="connection-note" role="status">{{ $t('settings.Connections.loading') }}</p>
      <p v-else-if="!rows.length && !error" class="connection-note">{{ $t('settings.Connections.noTokens') }}</p>
      <ul v-else class="connection-list"><li v-for="row in rows" :key="row.id"><div class="connection-row"><div><strong>{{ row.name }}</strong><small>{{ row.scopes.map(key => $t(`settings.Connections.${key}`)).join(' · ') }} · {{ row.expires_at ? new Date(row.expires_at).toLocaleDateString() : $t('settings.Connections.never') }}</small></div><button class="btn btn--ghost" :disabled="busy" @click="revoke(row.id)">{{ $t('settings.Connections.revoke') }}</button></div></li></ul>
    </div>
  </section>
</template>
