<script setup lang="ts">
import { ref, reactive } from 'vue'
import { useSettingsScope } from '../composables/useSettingsScope'
import { MessagePlugin } from '@/shared/ui/feedback'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { userApi } from '@/domains/user/infrastructure/userApi'
import { validatePasswordChange, MIN_PASSWORD_LENGTH } from '../../domain/validation'

import { t } from '@/shared/i18n'
const authStore = useAuthStore()
const pw = reactive({ old: '', next: '', confirm: '', otp: '' })
// Shown once the server says the account has two-factor authentication.
const needOtp = ref(false)
const pwError = ref<string | null>(null)
const savingPw = ref(false)
const scope = useSettingsScope(() => { pw.old = pw.next = pw.confirm = pw.otp = ''; needOtp.value = false; pwError.value = null; savingPw.value = false })

async function savePassword() {
  if (savingPw.value) return
  const current = scope()
  pwError.value = validatePasswordChange(pw.old, pw.next, pw.confirm)
  if (pwError.value) return
  savingPw.value = true
  try {
    await userApi.changePassword(pw.old, pw.next, pw.otp.trim())
    if (!current()) return
    pw.old = pw.next = pw.confirm = pw.otp = ''
    MessagePlugin.success(t('settings.PasswordSettingsForm.s7'))
  } catch (e) {
    if (!current()) return
    const api = (e as { apiMessage?: string })?.apiMessage
    if (api === 'two_factor_required') { needOtp.value = true; pwError.value = t('settings.TwoFactor.codeRequired'); return }
    if (api === 'invalid_two_factor_code') { pw.otp = ''; pwError.value = t('settings.TwoFactor.wrongCode'); return }
    if (api === 'two_factor_locked') { pwError.value = t('settings.TwoFactor.locked'); return }
    const msg = e instanceof Error ? e.message : t('settings.PasswordSettingsForm.s8')
    pwError.value = msg === 'old password is wrong' ? t('settings.PasswordSettingsForm.s9') : msg
  } finally {
    if (current()) savingPw.value = false
  }
}
</script>

<template>
  <form class="settings-form" @submit.prevent="savePassword">
    <!-- hidden username helps password managers pick the right entry -->
    <input type="text" class="sr-only" autocomplete="username" :value="authStore.currentUser?.username" tabindex="-1" aria-hidden="true" readonly />
    <div class="settings-field">
      <label for="set-pw-old" class="settings-field__label">{{ $t('settings.PasswordSettingsForm.s1') }}</label>
      <input id="set-pw-old" v-model="pw.old" class="settings-input" type="password" autocomplete="current-password" />
    </div>
    <div class="settings-field">
      <label for="set-pw-new" class="settings-field__label">{{ $t('settings.PasswordSettingsForm.s2') }}</label>
      <input id="set-pw-new" v-model="pw.next" class="settings-input" type="password" autocomplete="new-password" aria-describedby="set-pw-hint" />
      <span id="set-pw-hint" class="settings-field__hint">{{ $t('settings.PasswordSettingsForm.s3', [MIN_PASSWORD_LENGTH]) }}</span>
    </div>
    <div class="settings-field">
      <label for="set-pw-confirm" class="settings-field__label">{{ $t('settings.PasswordSettingsForm.s4') }}</label>
      <input id="set-pw-confirm" v-model="pw.confirm" class="settings-input" type="password" autocomplete="new-password" />
    </div>
    <div v-if="needOtp" class="settings-field">
      <label for="set-pw-otp" class="settings-field__label">{{ $t('settings.TwoFactor.codeOrBackup') }}</label>
      <input id="set-pw-otp" v-model="pw.otp" class="settings-input" autocomplete="one-time-code" autocapitalize="off" spellcheck="false" maxlength="32" />
    </div>
    <p v-if="pwError" class="settings-error" role="alert">{{ pwError }}</p>
    <div class="settings-actions">
      <button type="submit" class="btn btn--primary btn--sm" :disabled="savingPw">
        {{ savingPw ? $t('settings.PasswordSettingsForm.s5') : $t('settings.PasswordSettingsForm.s6') }}
      </button>
    </div>
  </form>
</template>

<style scoped>
.settings-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.settings-field {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.settings-field__label {
  font-size: var(--text-footnote, var(--font-sm));
  font-weight: 600;
}

.settings-field__hint {
  font-size: var(--text-footnote, var(--font-xs));
  color: var(--color-label-secondary, var(--text-secondary));
}

.settings-input {
  width: 100%;
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-separator, var(--border-color));
  border-radius: var(--radius-sm, 8px);
  background: transparent;
  color: inherit;
  font: inherit;
  resize: vertical;
}

.settings-actions {
  display: flex;
  gap: var(--space-2);
  justify-content: flex-end;
}

.settings-error {
  color: var(--color-danger, var(--accent-red, #e0245e));
  font-size: var(--font-sm);
}
</style>
