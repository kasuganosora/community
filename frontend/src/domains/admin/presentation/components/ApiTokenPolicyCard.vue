<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { AdminAlert, AdminButton, AdminCheck } from './ui'
import { settingsAdminApi as api } from '../../infrastructure/settingsAdminApi'
import { t } from '@/shared/i18n'

const KEY = 'security.api_token_inactivity_expiry'
const enabled = ref(false), saved = ref(false), loaded = ref(false), busy = ref(false), error = ref(''), notice = ref('')
function apply(fields: { key: string; value?: unknown }[]) {
  const value = fields.find(f => f.key === KEY)?.value
  enabled.value = saved.value = value === true || value === 'true'
}
async function load() {
  error.value = ''
  try { apply((await api.get('api_tokens')).fields); loaded.value = true } catch { error.value = t('admin.ApiTokens.error') }
}
async function save() {
  if (busy.value) return
  busy.value = true; error.value = ''; notice.value = ''
  try { apply((await api.save('api_tokens', { [KEY]: enabled.value }, [])).fields); notice.value = t('admin.ApiTokens.saved') }
  catch { error.value = t('admin.ApiTokens.error') }
  finally { busy.value = false }
}
onMounted(load)
</script>
<template>
  <section class="admin-card" :aria-label="$t('admin.ApiTokens.title')">
    <h2>{{ $t('admin.ApiTokens.title') }}</h2>
    <AdminAlert v-if="error" tone="danger">{{ error }}</AdminAlert>
    <AdminAlert v-if="notice" tone="success">{{ notice }}</AdminAlert>
    <form v-if="loaded" class="admin-form" @submit.prevent="save">
      <AdminCheck v-model="enabled" variant="switch" :label="$t('admin.ApiTokens.inactivity')" :hint="$t('admin.ApiTokens.inactivityHint')" :disabled="busy" />
      <div class="admin-form-actions"><AdminButton type="submit" variant="primary" :loading="busy" :disabled="enabled === saved">{{ $t('admin.ApiTokens.save') }}</AdminButton></div>
    </form>
  </section>
</template>
