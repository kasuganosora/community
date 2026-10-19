<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { isAxiosError } from 'axios'
import BottomSheet from '@/shared/components/sheets/BottomSheet.vue'
import { connectionsApi, type AccessToken } from '../../infrastructure/connectionsApi'
import { useSettingsScope } from '../composables/useSettingsScope'
import { adminScopeRows, collapseScopes, needsReauth, scopeLabelKey, userScopeRows } from '../../domain/tokenScopes'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { formatRelativeTime } from '@/shared/utils/format'
import { t } from '@/shared/i18n'

const auth = useAuthStore()
const rows = ref<AccessToken[]>([])
const loading = ref(true), busy = ref(false), error = ref(''), formError = ref(''), name = ref(''), expires = ref(30), secret = ref(''), creating = ref(false), copied = ref(false)
const selected = ref<Set<string>>(new Set(['read:accounts', 'read:statuses']))
const password = ref(''), otp = ref('')
const isAdmin = computed(() => auth.currentUser?.is_admin === true || (auth.currentUser?.admin_permissions?.length ?? 0) > 0)
const chosen = computed(() => collapseScopes(selected.value))
const reauth = computed(() => needsReauth(chosen.value))
const canSubmit = computed(() => !busy.value && !!name.value.trim() && chosen.value.length > 0 && (!reauth.value || !!password.value))

function reset() { name.value = ''; password.value = ''; otp.value = ''; formError.value = ''; selected.value = new Set(['read:accounts', 'read:statuses']); expires.value = 30 }
const scope = useSettingsScope(() => { loading.value = false; rows.value = []; secret.value = ''; error.value = ''; busy.value = false; creating.value = false; reset() })

function toggle(key: string, on: boolean) { const next = new Set(selected.value); if (on) next.add(key); else next.delete(key); selected.value = next }
// "Enable all" never turns on admin scopes; "Disable all" clears everything.
function enableAll() { const next = new Set([...selected.value].filter(s => s.startsWith('admin:'))); userScopeRows.forEach(r => next.add(r.scope)); selected.value = next }
function disableAll() { selected.value = new Set() }
function open() { reset(); creating.value = true }

async function load() {
  const current = scope(); loading.value = true; error.value = ''
  try { const result = await connectionsApi.tokens(); if (current()) rows.value = result }
  catch { if (current()) error.value = t('settings.Connections.loadError') }
  finally { if (current()) loading.value = false }
}
async function create() {
  if (!canSubmit.value) return
  const current = scope(); busy.value = true; formError.value = ''; secret.value = ''; copied.value = false
  try {
    const result = await connectionsApi.createToken({ name: name.value.trim(), scopes: chosen.value, expires_in_days: expires.value, ...(reauth.value ? { password: password.value, otp: otp.value.trim() || undefined } : {}) })
    if (!current()) return
    rows.value.unshift(result.item); secret.value = result.token; creating.value = false; reset()
  } catch (e) {
    if (!current()) return
    const status = isAxiosError(e) ? e.response?.status : undefined
    const code = isAxiosError(e) ? (e.response?.data as { error?: string } | undefined)?.error : undefined
    formError.value = t(code === 'admin_scope_forbidden' ? 'settings.Connections.adminForbidden' : status === 422 && reauth.value && code !== 'invalid_scope' ? 'settings.Connections.passwordError' : 'settings.Connections.saveError')
  } finally { if (current()) { busy.value = false; password.value = '' } }
}
async function revoke(id: string) {
  if (busy.value || !window.confirm(t('settings.Connections.revokeConfirm'))) return
  const current = scope(); busy.value = true; error.value = ''
  try { await connectionsApi.revokeToken(id); if (current()) { rows.value = rows.value.filter(row => row.id !== id); secret.value = '' } }
  catch { if (current()) error.value = t('settings.Connections.saveError') }
  finally { if (current()) busy.value = false }
}
async function copy() { try { await navigator.clipboard.writeText(secret.value); copied.value = true } catch { error.value = t('settings.Connections.copyError') } }

function scopeSummary(scopes: string[]) {
  const parts: string[] = []
  const rest = scopes.filter(s => { if (s === 'read') { parts.push(t('settings.Connections.allRead')); return false } if (s === 'write') { parts.push(t('settings.Connections.allWrite')); return false } return true })
  if (rest.length <= 2) parts.push(...rest.map(s => t(scopeLabelKey(s))))
  else parts.push(t('settings.Connections.scopeCount', { count: rest.length }))
  return parts.join(' · ')
}
const lastUsed = (row: AccessToken) => row.last_used_at ? t('settings.Connections.lastUsed', { time: formatRelativeTime(row.last_used_at) }) : t('settings.Connections.neverUsed')
onMounted(load)
</script>
<template>
  <section class="connection-panel">
    <div class="connection-body" style="padding-top: 20px">
      <div class="connection-row"><h2 class="connection-section-title">{{ $t('settings.Connections.tokens') }}</h2><button class="btn btn--primary" :disabled="loading || busy" @click="open">{{ $t('settings.Connections.generate') }}</button></div>
      <p v-if="error" class="connection-error" role="alert">{{ error }} <button v-if="!busy" class="btn btn--ghost" @click="load">{{ $t('settings.Connections.retry') }}</button></p>
      <div v-if="secret" class="connection-secret" role="status"><strong>{{ $t('settings.Connections.saveSecret') }}</strong><textarea :value="secret" readonly :aria-label="$t('settings.Connections.token')" spellcheck="false" /><div class="connection-actions"><button class="btn btn--primary" @click="copy">{{ $t(copied ? 'settings.Connections.copied' : 'settings.Connections.copy') }}</button><button class="btn btn--ghost" @click="secret = ''">{{ $t('settings.Connections.close') }}</button></div></div>
      <p v-if="loading" class="connection-note" role="status">{{ $t('settings.Connections.loading') }}</p>
      <p v-else-if="!rows.length && !error" class="connection-note">{{ $t('settings.Connections.noTokens') }}</p>
      <ul v-else class="connection-list"><li v-for="row in rows" :key="row.id"><div class="connection-row"><div><strong>{{ row.name }}</strong><small class="token-meta" :title="row.scopes.join(' ')">{{ scopeSummary(row.scopes) }} · {{ row.expires_at ? new Date(row.expires_at).toLocaleDateString() : $t('settings.Connections.never') }}</small><small class="token-meta" :class="{ 'connection-error': row.revoked_reason }">{{ row.revoked_reason === 'inactive' ? $t('settings.Connections.expiredInactive') : lastUsed(row) }}</small></div><button class="btn btn--ghost" :disabled="busy" @click="revoke(row.id)">{{ $t('settings.Connections.revoke') }}</button></div></li></ul>
    </div>
    <BottomSheet v-model="creating" size="large" :title="$t('settings.Connections.generate')">
      <template #trailing><button type="submit" form="token-form" class="btn btn--primary token-done" :disabled="!canSubmit" :aria-busy="busy">{{ $t(busy ? 'settings.Connections.saving' : 'settings.Connections.done') }}</button></template>
      <form id="token-form" class="connection-form token-form" @submit.prevent="create">
        <p v-if="formError" class="connection-error" role="alert">{{ formError }}</p>
        <label>{{ $t('settings.Connections.name') }}<input v-model="name" required maxlength="100" autocomplete="off" :disabled="busy" /></label>
        <label>{{ $t('settings.Connections.expiry') }}<select v-model="expires" :disabled="busy"><option v-for="days in [7, 30, 90, 365]" :key="days" :value="days">{{ $t('settings.Connections.days', { count: days }) }}</option><option :value="0">{{ $t('settings.Connections.never') }}</option></select></label>
        <fieldset :disabled="busy" class="token-perms">
          <legend>{{ $t('settings.Connections.permissions') }}</legend>
          <div class="connection-actions"><button type="button" class="btn btn--secondary" @click="disableAll">{{ $t('settings.Connections.disableAll') }}</button><button type="button" class="btn btn--secondary" @click="enableAll">{{ $t('settings.Connections.enableAll') }}</button></div>
          <label v-for="row in userScopeRows" :key="row.scope" class="connection-toggle"><input type="checkbox" role="switch" :checked="selected.has(row.scope)" @change="toggle(row.scope, ($event.target as HTMLInputElement).checked)" /><span class="token-perm"><span>{{ $t(scopeLabelKey(row.scope)) }}</span><code>{{ row.scope }}</code></span></label>
        </fieldset>
        <fieldset v-if="isAdmin" :disabled="busy" class="token-perms token-perms--admin">
          <legend>{{ $t('settings.Connections.adminPermissions') }}</legend>
          <p class="connection-note">{{ $t('settings.Connections.adminHint') }}</p>
          <label v-for="row in adminScopeRows" :key="row.scope" class="connection-toggle"><input type="checkbox" role="switch" :checked="selected.has(row.scope)" @change="toggle(row.scope, ($event.target as HTMLInputElement).checked)" /><span class="token-perm"><span>{{ $t(scopeLabelKey(row.scope)) }}</span><code>{{ row.scope }}</code></span></label>
          <template v-if="reauth">
            <p class="connection-note">{{ $t('settings.Connections.confirmHint') }}</p>
            <label>{{ $t('settings.Connections.confirmPassword') }}<input v-model="password" type="password" autocomplete="current-password" required /></label>
            <label>{{ $t('settings.Connections.otp') }}<input v-model="otp" inputmode="numeric" autocomplete="one-time-code" maxlength="16" /></label>
          </template>
        </fieldset>
      </form>
    </BottomSheet>
  </section>
</template>
<style>
.token-form { padding: 4px 4px 24px; }
.token-perm { display: grid; gap: 2px; min-width: 0; }
.token-perm code { font-size: var(--text-caption1, 11px); color: var(--color-label-secondary); overflow-wrap: anywhere; }
.token-perms { display: grid; gap: 2px; }
.token-perms .connection-actions { margin-bottom: 8px; }
.connection-form fieldset.token-perms--admin { border: 1px solid var(--color-danger); border-radius: 12px; padding: 12px 16px; margin-top: 8px; }
.connection-form fieldset.token-perms--admin legend { color: var(--color-danger); padding: 0 6px; }
.token-meta { display: block; }
.token-done { min-height: 36px; padding-inline: 16px; }
.token-perms .connection-toggle input { appearance: none; flex: 0 0 38px; width: 38px; height: 22px; border: 1px solid var(--color-separator); border-radius: 20px; background: var(--color-fill); position: relative; margin: 0; cursor: pointer; }
.token-perms .connection-toggle input::after { content: ''; position: absolute; top: 3px; left: 3px; width: 14px; height: 14px; border-radius: 50%; background: var(--color-label-secondary); transition: transform .15s; }
.token-perms .connection-toggle input:checked { background: var(--color-accent); border-color: var(--color-accent); }
.token-perms .connection-toggle input:checked::after { transform: translateX(16px); background: #fff; }
.token-perms .connection-toggle input:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 3px; }
@media (prefers-reduced-motion: reduce) { .token-perms .connection-toggle input::after { transition: none; } }
</style>
