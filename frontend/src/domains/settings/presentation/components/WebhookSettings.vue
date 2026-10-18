<script setup lang="ts">
import { onMounted, ref, nextTick } from 'vue'
import { connectionsApi, type Webhook, type HookDraft, type Delivery } from '../../infrastructure/connectionsApi'
import { useSettingsScope } from '../composables/useSettingsScope'
import { t } from '@/shared/i18n'
const events = ['follow', 'followed', 'note', 'noteUpdated', 'noteDeleted', 'reply', 'renote', 'reaction', 'mention']
const blank = (): HookDraft => ({ name: '', url: '', secret: '', events: ['note', 'noteUpdated', 'noteDeleted', 'reply', 'followed', 'renote', 'reaction', 'mention'], enabled: true })
const rows = ref<Webhook[]>([]), loading = ref(true), busy = ref(false), error = ref(''), editing = ref<string | null>(null), draft = ref(blank()), secret = ref(''), notice = ref('')
const logId = ref(''), logs = ref<Delivery[]>([]), logLoading = ref(false)
const nameInput = ref<HTMLInputElement>()
const scope = useSettingsScope(() => { loading.value = false; logLoading.value = false; rows.value = []; logs.value = []; logId.value = ''; editing.value = null; draft.value = blank(); secret.value = ''; error.value = ''; notice.value = ''; busy.value = false })
async function load() {
  const current = scope(); loading.value = true; error.value = ''
  try { const result = await connectionsApi.hooks(); if (current()) rows.value = result }
  catch { if (current()) error.value = t('settings.Connections.loadError') }
  finally { if (current()) loading.value = false }
}
async function edit(row?: Webhook) {
  editing.value = row?.id ?? ''; draft.value = row ? { ...row, secret: '', events: [...row.events] } : blank(); secret.value = ''; error.value = ''
  await nextTick(); nameInput.value?.focus()
}
async function save() {
  if (busy.value || editing.value === null) return
  const current = scope(); busy.value = true; error.value = ''
  try {
    const result = await connectionsApi.saveHook(draft.value, editing.value || undefined)
    if (!current()) return
    rows.value = [result.item, ...rows.value.filter(row => row.id !== result.item.id)]; editing.value = null; draft.value = blank(); secret.value = result.secret; notice.value = t('settings.Connections.saved')
  } catch { if (current()) error.value = t('settings.Connections.hookSaveError') }
  finally { if (current()) busy.value = false }
}
async function remove(row: Webhook) {
  if (busy.value || !window.confirm(t('settings.Connections.deleteConfirm'))) return
  const current = scope(); busy.value = true; error.value = ''
  try { await connectionsApi.deleteHook(row.id); if (current()) { rows.value = rows.value.filter(hook => hook.id !== row.id); if (logId.value === row.id) logId.value = '' } }
  catch { if (current()) error.value = t('settings.Connections.saveError') }
  finally { if (current()) busy.value = false }
}
async function deliveries(id: string) {
  const current = scope(); logId.value = id; logLoading.value = true; logs.value = []; error.value = ''
  try { const result = await connectionsApi.deliveries(id); if (current() && logId.value === id) logs.value = result }
  catch { if (current() && logId.value === id) error.value = t('settings.Connections.loadError') }
  finally { if (current() && logId.value === id) logLoading.value = false }
}
async function test(row: Webhook) {
  if (busy.value) return
  const current = scope(); busy.value = true; error.value = ''; notice.value = ''
  try { await connectionsApi.testHook(row.id); if (current()) { notice.value = t('settings.Connections.testQueued'); await deliveries(row.id) } }
  catch { if (current()) error.value = t('settings.Connections.testError') }
  finally { if (current()) busy.value = false }
}
onMounted(load)
</script>

<template>
  <section class="connection-panel">
    <div class="connection-body" style="padding-top: 20px">
      <div class="connection-row"><h2 class="connection-section-title">Webhook</h2><div class="connection-actions"><button class="btn btn--ghost" :disabled="loading || busy" :aria-label="$t('settings.Connections.refresh')" @click="load"><t-icon name="refresh" size="18" aria-hidden="true" /></button><button v-if="editing === null" class="btn btn--primary" :disabled="loading || busy" @click="edit()">{{ $t('settings.Connections.createHook') }}</button></div></div>
      <p v-if="error" class="connection-error" role="alert">{{ error }}</p>
      <p v-if="notice" class="connection-note" role="status">{{ notice }}</p>
      <div v-if="secret" class="connection-secret" role="status"><strong>{{ $t('settings.Connections.saveSecret') }}</strong><textarea :value="secret" readonly :aria-label="$t('settings.Connections.secret')" spellcheck="false" /><button class="btn btn--ghost" @click="secret = ''">{{ $t('settings.Connections.close') }}</button></div>
      <form v-if="editing !== null" class="connection-form" @submit.prevent="save">
        <h3 class="connection-section-title">{{ $t(editing ? 'settings.Connections.editHook' : 'settings.Connections.createHook') }}</h3>
        <label>{{ $t('settings.Connections.name') }}<input ref="nameInput" v-model="draft.name" required maxlength="100" autocomplete="off" :disabled="busy" /></label>
        <label>URL<input v-model="draft.url" required type="url" placeholder="https://example.com/webhook" maxlength="2048" autocomplete="off" :disabled="busy" /></label>
        <label>{{ $t('settings.Connections.secret') }}<input v-model="draft.secret" type="password" maxlength="256" autocomplete="new-password" :disabled="busy" /><small class="connection-note">{{ $t(editing ? 'settings.Connections.keepSecret' : 'settings.Connections.generateSecret') }}</small></label>
        <fieldset :disabled="busy"><legend>{{ $t('settings.Connections.triggers') }}</legend><label v-for="event in events" :key="event" class="connection-toggle"><input v-model="draft.events" type="checkbox" :value="event" role="switch" /><span>{{ $t(`settings.Connections.events.${event}`) }}</span></label></fieldset>
        <label class="connection-toggle"><input v-model="draft.enabled" type="checkbox" role="switch" :disabled="busy" />{{ $t('settings.Connections.enabled') }}</label>
        <div class="connection-actions"><button class="btn btn--primary" :disabled="busy || !draft.name.trim() || !draft.url || !draft.events.length" :aria-busy="busy">{{ $t(busy ? 'settings.Connections.saving' : 'settings.Connections.save') }}</button><button type="button" class="btn btn--ghost" :disabled="busy" @click="editing = null; draft = blank()">{{ $t('settings.Connections.cancel') }}</button></div>
      </form>
      <p v-if="loading" class="connection-note" role="status">{{ $t('settings.Connections.loading') }}</p>
      <p v-else-if="!rows.length && !error" class="connection-note">{{ $t('settings.Connections.noHooks') }}</p>
      <ul class="connection-list">
<li v-for="row in rows" :key="row.id">
        <div class="connection-row"><div><strong>{{ row.name }} · {{ $t(row.enabled ? 'settings.Connections.enabled' : 'settings.Connections.disabled') }}</strong><small>{{ row.url }}</small></div><button class="btn btn--ghost" :disabled="busy" @click="edit(row)">{{ $t('settings.Connections.edit') }}</button></div>
        <p class="connection-note">{{ row.events.map(event => $t(`settings.Connections.events.${event}`)).join(' · ') }}</p>
        <div class="connection-actions"><button class="btn btn--ghost" :disabled="busy || !row.enabled" @click="test(row)">{{ $t('settings.Connections.test') }}</button><button class="btn btn--ghost" :disabled="logLoading" @click="logId === row.id ? logId = '' : deliveries(row.id)">{{ $t('settings.Connections.deliveries') }}</button><button class="btn btn--ghost" :disabled="busy" @click="remove(row)">{{ $t('settings.Connections.delete') }}</button></div>
        <div v-if="logId === row.id" class="hook-deliveries">
          <div class="connection-row"><strong>{{ $t('settings.Connections.recentDeliveries') }}</strong><button class="btn btn--ghost" :disabled="logLoading" @click="deliveries(row.id)">{{ $t('settings.Connections.refresh') }}</button></div>
          <p v-if="logLoading" role="status" class="connection-note">{{ $t('settings.Connections.loading') }}</p><p v-else-if="!logs.length" class="connection-note">{{ $t('settings.Connections.noDeliveries') }}</p>
          <ul v-else class="connection-list"><li v-for="entry in logs" :key="entry.id"><div class="connection-row"><span>{{ $t(`settings.Connections.events.${entry.event}`) }}</span><strong :class="{ 'connection-error': entry.state === 'failed' }">{{ $t(`settings.Connections.states.${entry.state}`) }}</strong></div><small class="connection-note">{{ new Date(entry.created_at).toLocaleString() }} · {{ $t('settings.Connections.attempts', { count: entry.attempts }) }}</small><small v-if="entry.last_error" class="connection-note">{{ entry.last_error }}</small></li></ul>
        </div>
      </li>
</ul>
    </div>
  </section>
</template>
<style scoped>.hook-deliveries { border-radius: 12px; padding: 16px; background: var(--color-bg); }</style>
