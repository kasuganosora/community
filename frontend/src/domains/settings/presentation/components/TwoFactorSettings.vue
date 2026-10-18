<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue'
import { useSettingsScope } from '../composables/useSettingsScope'
import { MessagePlugin } from '@/shared/ui/feedback'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { twoFactorApi, type TwoFactorStatus } from '../../infrastructure/twoFactorApi'
import { t } from '@/shared/i18n'

type Mode = 'idle' | 'password' | 'scan' | 'codes' | 'regenerate' | 'disable'

const authStore = useAuthStore()
const status = ref<TwoFactorStatus | null>(null)
const loadError = ref('')
const mode = ref<Mode>('idle')
const busy = ref(false)
const error = ref('')
const password = ref('')
const code = ref('')
const secret = ref('')
const qrSvg = ref('')
const backupCodes = ref<string[]>([])
const codeField = ref<HTMLInputElement | null>(null)

function resetForm() {
  password.value = ''
  code.value = ''
  error.value = ''
}
const scope = useSettingsScope(() => {
  status.value = null; mode.value = 'idle'; busy.value = false; secret.value = ''; qrSvg.value = ''; backupCodes.value = []; resetForm()
})

const groupedSecret = computed(() => secret.value.replace(/(.{4})/g, '$1 ').trim())
const enabledSince = computed(() => status.value?.enabled_at ? new Date(status.value.enabled_at).toLocaleDateString() : '')

function messageOf(e: unknown) {
  const api = (e as { apiMessage?: string })?.apiMessage
  switch (api) {
    case 'invalid password': return t('settings.TwoFactor.wrongPassword')
    case 'invalid_two_factor_code': return t('settings.TwoFactor.wrongCode')
    case 'two_factor_required': return t('settings.TwoFactor.codeRequired')
    case 'two_factor_locked': return t('settings.TwoFactor.locked')
    case 'two_factor_setup_expired': return t('settings.TwoFactor.setupExpired')
  }
  return e instanceof Error ? e.message : t('settings.TwoFactor.failed')
}

async function refresh() {
  const current = scope()
  loadError.value = ''
  try {
    const value = await twoFactorApi.status()
    if (current()) status.value = value
  } catch {
    if (current()) loadError.value = t('settings.TwoFactor.loadFailed')
  }
}

function start(next: Mode) {
  resetForm()
  mode.value = next
}

async function renderQR(uri: string) {
  const { default: createQRCode } = await import('qrcode-generator')
  const qr = createQRCode(0, 'M')
  qr.addData(uri)
  qr.make()
  qrSvg.value = qr.createSvgTag({ cellSize: 5, margin: 16, scalable: true })
}

async function beginSetup() {
  if (busy.value || !password.value) return
  const current = scope()
  busy.value = true
  error.value = ''
  try {
    const res = await twoFactorApi.setup(password.value)
    if (!current()) return
    secret.value = res.secret
    password.value = ''
    await renderQR(res.otpauth_uri).catch(() => { qrSvg.value = '' })
    mode.value = 'scan'
    await nextTick()
    codeField.value?.focus()
  } catch (e) {
    if (current()) error.value = messageOf(e)
  } finally {
    if (current()) busy.value = false
  }
}

async function confirmSetup() {
  if (busy.value || !code.value.trim()) return
  const current = scope()
  busy.value = true
  error.value = ''
  try {
    const res = await twoFactorApi.confirm(code.value.trim())
    if (!current()) return
    secret.value = ''
    qrSvg.value = ''
    backupCodes.value = res.backup_codes
    mode.value = 'codes'
    code.value = ''
    MessagePlugin.success(t('settings.TwoFactor.enabled'))
    await refresh()
  } catch (e) {
    if (current()) error.value = messageOf(e)
  } finally {
    if (current()) busy.value = false
  }
}

async function regenerate() {
  if (busy.value || !password.value || !code.value.trim()) return
  const current = scope()
  busy.value = true
  error.value = ''
  try {
    const res = await twoFactorApi.regenerateBackupCodes(password.value, code.value.trim())
    if (!current()) return
    backupCodes.value = res.backup_codes
    resetForm()
    mode.value = 'codes'
    await refresh()
  } catch (e) {
    if (current()) error.value = messageOf(e)
  } finally {
    if (current()) busy.value = false
  }
}

async function disable() {
  if (busy.value || !password.value || !code.value.trim()) return
  const current = scope()
  busy.value = true
  error.value = ''
  try {
    await twoFactorApi.disable(password.value, code.value.trim())
    if (!current()) return
    resetForm()
    mode.value = 'idle'
    MessagePlugin.success(t('settings.TwoFactor.disabled'))
    await refresh()
  } catch (e) {
    if (current()) error.value = messageOf(e)
  } finally {
    if (current()) busy.value = false
  }
}

async function copyCodes() {
  try {
    await navigator.clipboard.writeText(backupCodes.value.join('\n'))
    MessagePlugin.success(t('settings.TwoFactor.copied'))
  } catch {
    MessagePlugin.error(t('settings.TwoFactor.copyFailed'))
  }
}

function downloadCodes() {
  const host = window.location.host
  const body = `${host} — ${authStore.currentUser?.username ?? ''}\n\n${backupCodes.value.join('\n')}\n`
  const url = URL.createObjectURL(new Blob([body], { type: 'text/plain' }))
  const link = document.createElement('a')
  link.href = url
  link.download = `${host}-backup-codes.txt`
  link.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

function finishCodes() {
  backupCodes.value = []
  mode.value = 'idle'
}

onMounted(() => { refresh().catch(() => {}) })
</script>

<template>
  <div class="two-factor" data-testid="two-factor-settings">
    <div v-if="loadError" class="settings-error" role="alert">{{ loadError }} <button type="button" class="btn btn--ghost" @click="refresh">{{ $t('settings.TwoFactor.retry') }}</button></div>
    <p v-else-if="!status" class="hint" role="status">{{ $t('settings.TwoFactor.loading') }}</p>

    <template v-else>
      <p class="two-factor__state" :class="{ 'two-factor__state--on': status.enabled }">
        <span class="two-factor__dot" aria-hidden="true" />
        <span v-if="status.enabled">{{ $t('settings.TwoFactor.statusOn', [enabledSince]) }}</span>
        <span v-else>{{ $t('settings.TwoFactor.statusOff') }}</span>
      </p>
      <p v-if="status.enabled && mode !== 'codes'" class="hint">{{ $t('settings.TwoFactor.remaining', [status.backup_codes_remaining]) }}</p>
      <p v-if="!status.enabled && mode === 'idle'" class="hint">{{ $t('settings.TwoFactor.intro') }}</p>

      <!-- backup codes, shown exactly once -->
      <div v-if="mode === 'codes'" class="two-factor__codes" data-testid="backup-codes">
        <p class="two-factor__warn" role="alert">{{ $t('settings.TwoFactor.codesOnce') }}</p>
        <ol class="two-factor__list">
          <li v-for="c in backupCodes" :key="c"><code>{{ c }}</code></li>
        </ol>
        <div class="settings-actions two-factor__row">
          <button type="button" class="btn btn--sm" @click="copyCodes">{{ $t('settings.TwoFactor.copy') }}</button>
          <button type="button" class="btn btn--sm" @click="downloadCodes">{{ $t('settings.TwoFactor.download') }}</button>
          <button type="button" class="btn btn--primary btn--sm" @click="finishCodes">{{ $t('settings.TwoFactor.saved') }}</button>
        </div>
      </div>

      <!-- enable: password -->
      <form v-else-if="mode === 'password'" class="settings-form" @submit.prevent="beginSetup">
        <input type="text" class="sr-only" autocomplete="username" :value="authStore.currentUser?.username" tabindex="-1" aria-hidden="true" readonly />
        <div class="settings-field">
          <label for="tf-setup-password" class="settings-field__label">{{ $t('settings.TwoFactor.password') }}</label>
          <input id="tf-setup-password" v-model="password" class="settings-input" type="password" autocomplete="current-password" required />
        </div>
        <p v-if="error" class="settings-error" role="alert">{{ error }}</p>
        <div class="settings-actions two-factor__row">
          <button type="button" class="btn btn--sm" @click="start('idle')">{{ $t('settings.TwoFactor.cancel') }}</button>
          <button type="submit" class="btn btn--primary btn--sm" :disabled="busy || !password">{{ $t('settings.TwoFactor.continue') }}</button>
        </div>
      </form>

      <!-- enable: scan + confirm -->
      <form v-else-if="mode === 'scan'" class="settings-form" data-testid="two-factor-scan" @submit.prevent="confirmSetup">
        <p class="hint">{{ $t('settings.TwoFactor.scan') }}</p>
        <!-- eslint-disable-next-line vue/no-v-html -- SVG generated locally by qrcode-generator from the otpauth URI -->
        <div v-if="qrSvg" class="two-factor__qr" role="img" :aria-label="$t('settings.TwoFactor.qrLabel')" v-html="qrSvg" />
        <div class="settings-field">
          <span class="settings-field__label">{{ $t('settings.TwoFactor.manualKey') }}</span>
          <code class="two-factor__secret" data-testid="two-factor-secret">{{ groupedSecret }}</code>
        </div>
        <div class="settings-field">
          <label for="tf-confirm-code" class="settings-field__label">{{ $t('settings.TwoFactor.code') }}</label>
          <input id="tf-confirm-code" ref="codeField" v-model="code" class="settings-input two-factor__code" inputmode="numeric" autocomplete="one-time-code" maxlength="6" pattern="[0-9 ]*" required />
        </div>
        <p v-if="error" class="settings-error" role="alert">{{ error }}</p>
        <div class="settings-actions two-factor__row">
          <button type="button" class="btn btn--sm" @click="start('idle')">{{ $t('settings.TwoFactor.cancel') }}</button>
          <button type="submit" class="btn btn--primary btn--sm" :disabled="busy || code.trim().length < 6">{{ $t('settings.TwoFactor.verify') }}</button>
        </div>
      </form>

      <!-- regenerate / disable: password + code -->
      <form v-else-if="mode === 'regenerate' || mode === 'disable'" class="settings-form" @submit.prevent="mode === 'disable' ? disable() : regenerate()">
        <input type="text" class="sr-only" autocomplete="username" :value="authStore.currentUser?.username" tabindex="-1" aria-hidden="true" readonly />
        <p class="hint">{{ mode === 'disable' ? $t('settings.TwoFactor.disableHint') : $t('settings.TwoFactor.regenerateHint') }}</p>
        <div class="settings-field">
          <label for="tf-act-password" class="settings-field__label">{{ $t('settings.TwoFactor.password') }}</label>
          <input id="tf-act-password" v-model="password" class="settings-input" type="password" autocomplete="current-password" required />
        </div>
        <div class="settings-field">
          <label for="tf-act-code" class="settings-field__label">{{ $t('settings.TwoFactor.codeOrBackup') }}</label>
          <input id="tf-act-code" v-model="code" class="settings-input" autocomplete="one-time-code" autocapitalize="off" spellcheck="false" maxlength="32" required />
        </div>
        <p v-if="error" class="settings-error" role="alert">{{ error }}</p>
        <div class="settings-actions two-factor__row">
          <button type="button" class="btn btn--sm" @click="start('idle')">{{ $t('settings.TwoFactor.cancel') }}</button>
          <button type="submit" class="btn btn--sm" :class="mode === 'disable' ? 'two-factor__danger' : 'btn--primary'" :disabled="busy || !password || !code.trim()">
            {{ mode === 'disable' ? $t('settings.TwoFactor.disable') : $t('settings.TwoFactor.regenerate') }}
          </button>
        </div>
      </form>

      <div v-else class="settings-actions two-factor__row">
        <button v-if="!status.enabled" type="button" class="btn btn--primary btn--sm" data-testid="two-factor-enable" @click="start('password')">{{ $t('settings.TwoFactor.enable') }}</button>
        <template v-else>
          <button type="button" class="btn btn--sm" @click="start('regenerate')">{{ $t('settings.TwoFactor.regenerate') }}</button>
          <button type="button" class="btn btn--sm two-factor__danger" @click="start('disable')">{{ $t('settings.TwoFactor.disable') }}</button>
        </template>
      </div>
      <p class="hint">{{ $t('settings.TwoFactor.passkeyNote') }}</p>
    </template>
  </div>
</template>

<style scoped>
.two-factor { display: flex; flex-direction: column; gap: var(--space-3); }
.hint { color: var(--color-label-secondary); font-size: var(--text-footnote); margin: 0; }
.settings-form { display: flex; flex-direction: column; gap: var(--space-3); }
.settings-field { display: flex; flex-direction: column; gap: var(--space-1); }
.settings-field__label { font-size: var(--text-footnote, var(--font-sm)); font-weight: 600; }
.settings-input {
  width: 100%;
  min-height: var(--hit-target, 44px);
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-separator, var(--border-color));
  border-radius: var(--radius-sm, 8px);
  background: transparent;
  color: inherit;
  font: inherit;
}
.settings-error { color: var(--color-danger, #e0245e); font-size: var(--font-sm); margin: 0; }
.two-factor__row { display: flex; flex-wrap: wrap; gap: var(--space-2); }
.two-factor__state { display: flex; align-items: center; gap: var(--space-2); margin: 0; font-weight: 600; }
.two-factor__dot { width: 10px; height: 10px; border-radius: 50%; background: var(--color-label-tertiary, #999); flex: none; }
.two-factor__state--on .two-factor__dot { background: var(--color-success, #34c759); }
.two-factor__qr { width: min(220px, 70vw); aspect-ratio: 1; background: #fff; border-radius: var(--radius-md, 10px); padding: 4px; }
.two-factor__qr :deep(svg) { width: 100%; height: 100%; display: block; }
.two-factor__secret { font-family: var(--font-mono, ui-monospace, monospace); font-size: 1rem; word-break: break-all; user-select: all; padding: var(--space-2); border-radius: var(--radius-sm, 6px); background: var(--color-fill-tertiary, rgba(120, 120, 128, .12)); }
.two-factor__code { letter-spacing: .2em; font-variant-numeric: tabular-nums; max-width: 12rem; }
.two-factor__codes { display: flex; flex-direction: column; gap: var(--space-3); }
.two-factor__warn { margin: 0; color: var(--color-warning, #b25000); font-weight: 600; }
.two-factor__list { display: grid; grid-template-columns: repeat(auto-fill, minmax(9rem, 1fr)); gap: var(--space-2); margin: 0; padding-left: 1.5rem; }
.two-factor__list code { font-family: var(--font-mono, ui-monospace, monospace); font-size: 1rem; }
.two-factor__danger { color: var(--color-danger); }
</style>
