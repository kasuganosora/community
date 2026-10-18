<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { runConsole } from '../../infrastructure/connectionsApi'
import { useSettingsScope } from '../composables/useSettingsScope'
import { t } from '@/shared/i18n'
const path = ref('/api/v1/accounts/verify_credentials'), method = ref('GET'), body = ref('{}'), token = ref(''), busy = ref(false), error = ref('')
const result = ref<Awaited<ReturnType<typeof runConsole>> | null>(null)
let controller: AbortController | undefined
const scope = useSettingsScope(() => { controller?.abort(); token.value = ''; body.value = '{}'; result.value = null; busy.value = false; error.value = '' })
onBeforeUnmount(() => controller?.abort())
async function run() {
  if (busy.value) return
  if (method.value !== 'GET' && !window.confirm(t('settings.Connections.requestConfirm'))) return
  const current = scope(); busy.value = true; error.value = ''; result.value = null
  const requestController = new AbortController()
  controller = requestController
  const timer = setTimeout(() => requestController.abort(), 20000)
  try { const response = await runConsole({ path: path.value.trim(), method: method.value, body: body.value, token: token.value }, requestController.signal); if (current()) result.value = response }
  catch (e) {
    if (current()) error.value = t(e instanceof SyntaxError ? 'settings.Connections.jsonError' : e instanceof Error && e.message === 'path' ? 'settings.Connections.pathError' : e instanceof Error && e.message === 'size' ? 'settings.Connections.sizeError' : 'settings.Connections.requestError')
  } finally { clearTimeout(timer); if (current()) busy.value = false }
}
</script>
<template>
  <form class="connection-form" @submit.prevent="run">
    <p class="connection-note">{{ $t('settings.Connections.consoleHint') }}</p>
    <label>{{ $t('settings.Connections.method') }}<select v-model="method" :disabled="busy"><option v-for="value in ['GET', 'POST', 'PUT', 'PATCH', 'DELETE']" :key="value">{{ value }}</option></select></label>
    <label>{{ $t('settings.Connections.path') }}<input v-model="path" required placeholder="/api/v1/accounts/verify_credentials" :disabled="busy" spellcheck="false" /></label>
    <label>{{ $t('settings.Connections.optionalToken') }}<input v-model="token" type="password" autocomplete="off" :disabled="busy" /><small class="connection-note">{{ $t('settings.Connections.currentSession') }}</small></label>
    <label v-if="method !== 'GET'">JSON<textarea v-model="body" :disabled="busy" spellcheck="false" /></label>
    <div class="connection-actions"><button class="btn btn--primary" :disabled="busy" :aria-busy="busy">{{ $t(busy ? 'settings.Connections.running' : 'settings.Connections.run') }}</button><button v-if="busy" type="button" class="btn btn--ghost" @click="controller?.abort()">{{ $t('settings.Connections.cancel') }}</button></div>
    <p v-if="error" role="alert" class="connection-error">{{ error }}</p>
    <div v-if="result" class="console-response" aria-live="polite"><strong>HTTP {{ result.status }} · {{ result.duration }} ms</strong><pre tabindex="0">{{ result.text || $t('settings.Connections.emptyResponse') }}</pre></div>
  </form>
</template>
<style scoped>.console-response { min-width: 0; }.console-response pre { margin-bottom: 0; max-height: 400px; overflow: auto; white-space: pre-wrap; overflow-wrap: anywhere; padding: 16px; background: var(--color-bg); border-radius: 12px; font-size: 13px; }</style>
