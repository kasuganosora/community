<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { accountLifecycleApi, type AccountLifecycleState } from '../../infrastructure/accountLifecycleApi'
import { useSettingsScope } from '../composables/useSettingsScope'
import { t } from '@/shared/i18n'

const auth = useAuthStore()
const state = ref<AccountLifecycleState | null>(null)
const aliases = ref<string[]>([])
const target = ref(''), movePassword = ref(''), deletePassword = ref(''), confirmation = ref('')
const moveConfirmed = ref(false), deleteConfirmed = ref(false)
// Accounts with two-factor authentication also confirm with a code.
const otp = ref(''), needOtp = ref(false)
const loading = ref(false), busy = ref(''), error = ref(''), notice = ref('')
const key = (name: string) => t(`settings.AccountLifecycle.${name}`)
const scope = useSettingsScope(() => {
  state.value = null; aliases.value = []; otp.value = ''; needOtp.value = false; target.value = ''; movePassword.value = ''; deletePassword.value = ''; confirmation.value = ''
  moveConfirmed.value = deleteConfirmed.value = false; loading.value = false; busy.value = error.value = notice.value = ''
})
const disabled = computed(() => loading.value || !!busy.value || !state.value || state.value.deletion_requested)
const changed = computed(() => JSON.stringify(aliases.value.filter(a => a.trim()).map(a => a.trim())) !== JSON.stringify(state.value?.aliases ?? []))
function apply(value: AccountLifecycleState) { state.value = value; aliases.value = [...(value.aliases ?? [])] }
function failure(e: unknown) {
  const api = (e as { apiMessage?: string })?.apiMessage
  if (api === 'two_factor_required') { needOtp.value = true; return t('settings.TwoFactor.codeRequired') }
  if (api === 'invalid_two_factor_code') { otp.value = ''; return t('settings.TwoFactor.wrongCode') }
  if (api === 'two_factor_locked') return t('settings.TwoFactor.locked')
  const message = e instanceof Error ? e.message : ''
  const errors: Record<string, string> = {
    'invalid password': 'invalidPassword',
    'target account must list this account as an alias': 'aliasRequired',
    'cannot delete the last active administrator': 'lastAdmin',
    'account cannot be migrated': 'cannotMove',
    'alias account could not be resolved': 'unresolvedAlias',
    'target account could not be resolved': 'unresolvedTarget',
  }
  return key(errors[message] ?? 'failed')
}
async function load() {
  if (loading.value || busy.value) return
  const current = scope(); loading.value = true; error.value = ''
  try { const value = await accountLifecycleApi.get(); if (current()) apply(value) }
  catch (e) { if (current()) error.value = failure(e) }
  finally { if (current()) loading.value = false }
}
async function saveAliases() {
  if (disabled.value || state.value?.moved_to || !changed.value) return
  const current = scope(); busy.value = 'aliases'; error.value = notice.value = ''
  try {
    const value = await accountLifecycleApi.saveAliases(aliases.value.filter(a => a.trim()).map(a => a.trim()))
    if (!current()) return
    apply(value); notice.value = key('saved')
  } catch (e) { if (current()) error.value = failure(e) }
  finally { if (current()) busy.value = '' }
}
async function move() {
  if (disabled.value || state.value?.moved_to || !moveConfirmed.value || !target.value.trim() || !movePassword.value) return
  const current = scope(); busy.value = 'move'; error.value = notice.value = ''
  try {
    const value = await accountLifecycleApi.move(target.value.trim(), movePassword.value, otp.value.trim())
    if (!current()) return
    apply(value); movePassword.value = ''; otp.value = ''; moveConfirmed.value = false; notice.value = key('moved')
  } catch (e) { if (current()) error.value = failure(e) }
  finally { if (current()) busy.value = '' }
}
async function remove() {
  if (disabled.value || !deleteConfirmed.value || !deletePassword.value || confirmation.value !== auth.currentUser?.username) return
  const current = scope(); busy.value = 'delete'; error.value = notice.value = ''
  try {
    await accountLifecycleApi.delete(deletePassword.value, confirmation.value, otp.value.trim())
    if (!current()) return
    deletePassword.value = ''; notice.value = key('deletionQueued')
    await auth.logout(true, false)
  } catch (e) { if (current()) error.value = failure(e) }
  finally { if (current()) busy.value = '' }
}
onMounted(load)
</script>

<template>
  <section class="lifecycle" :aria-busy="loading || !!busy">
    <p v-if="loading" class="lifecycle__status" role="status">{{ key('loading') }}</p>
    <div v-if="error" class="lifecycle__error" role="alert">
      {{ error }}
      <button v-if="!state" class="btn btn--secondary btn--sm" :disabled="loading" @click="load">{{ key('retry') }}</button>
    </div>
    <p v-if="notice" class="lifecycle__notice" role="status">{{ notice }}</p>
    <details class="lifecycle__section" open>
      <summary>{{ key('migration') }}</summary>
      <div class="lifecycle__body">
        <p v-if="state?.moved_to" class="lifecycle__notice">{{ key('movedTo') }} <a :href="state.moved_to" target="_blank" rel="noopener noreferrer">{{ state.moved_to }}</a></p>
        <details class="lifecycle__subsection" :open="!state?.moved_to">
          <summary>{{ key('incoming') }}</summary>
          <form class="lifecycle__form" @submit.prevent="saveAliases">
            <p class="lifecycle__hint">{{ key('incomingHint') }}</p>
            <fieldset :disabled="disabled || !!state?.moved_to">
              <div v-for="(_, index) in aliases" :key="index" class="lifecycle__alias">
                <label :for="`migration-alias-${index}`">{{ key('oldAccount') }} {{ index + 1 }}</label>
                <div class="lifecycle__input-row">
                  <input :id="`migration-alias-${index}`" v-model="aliases[index]" class="settings-input" type="text" maxlength="2048" placeholder="@username@server.example" autocomplete="off" autocapitalize="none" spellcheck="false" />
                  <button class="btn btn--secondary btn--sm" type="button" :aria-label="`${key('remove')} ${index + 1}`" @click="aliases.splice(index, 1)">{{ key('remove') }}</button>
                </div>
              </div>
              <p v-if="!aliases.length" class="lifecycle__hint">{{ key('noAliases') }}</p>
              <div class="lifecycle__actions">
                <button type="button" class="btn btn--secondary btn--sm" :disabled="aliases.length >= 10" @click="aliases.push('')">{{ key('add') }}</button>
                <button type="submit" class="btn btn--primary btn--sm" :disabled="!changed">{{ key(busy === 'aliases' ? 'saving' : 'save') }}</button>
              </div>
            </fieldset>
          </form>
        </details>
        <details v-if="!state?.moved_to" class="lifecycle__subsection">
          <summary>{{ key('outgoing') }}</summary>
          <form class="lifecycle__form" @submit.prevent="move">
            <p class="lifecycle__hint">{{ key('outgoingHint') }}</p>
            <fieldset :disabled="disabled">
              <label for="migration-target">{{ key('newAccount') }}</label>
              <input id="migration-target" v-model="target" class="settings-input" required maxlength="2048" placeholder="@username@server.example" autocomplete="off" autocapitalize="none" spellcheck="false" />
              <label for="migration-password">{{ key('password') }}</label>
              <input id="migration-password" v-model="movePassword" class="settings-input" required type="password" autocomplete="current-password" />
              <template v-if="needOtp">
                <label for="migration-otp">{{ $t('settings.TwoFactor.codeOrBackup') }}</label>
                <input id="migration-otp" v-model="otp" class="settings-input" required autocomplete="one-time-code" autocapitalize="off" spellcheck="false" maxlength="32" />
              </template>
              <label class="lifecycle__check"><input v-model="moveConfirmed" type="checkbox" required /> <span>{{ key('moveConfirm') }}</span></label>
              <div class="lifecycle__actions"><button class="btn btn--primary btn--sm" :disabled="!moveConfirmed || !target.trim() || !movePassword">{{ key(busy === 'move' ? 'moving' : 'move') }}</button></div>
            </fieldset>
          </form>
        </details>
      </div>
    </details>
    <details class="lifecycle__section lifecycle__section--danger">
      <summary>{{ key('deletion') }}</summary>
      <form class="lifecycle__form" @submit.prevent="remove">
        <p class="lifecycle__warning">{{ key('deletionHint') }}</p>
        <fieldset :disabled="disabled">
          <label for="deletion-password">{{ key('password') }}</label>
          <input id="deletion-password" v-model="deletePassword" class="settings-input" required type="password" autocomplete="current-password" />
          <template v-if="needOtp">
            <label for="deletion-otp">{{ $t('settings.TwoFactor.codeOrBackup') }}</label>
            <input id="deletion-otp" v-model="otp" class="settings-input" required autocomplete="one-time-code" autocapitalize="off" spellcheck="false" maxlength="32" />
          </template>
          <label for="deletion-confirmation">{{ key('typeUsername') }} <strong>{{ auth.currentUser?.username }}</strong></label>
          <input id="deletion-confirmation" v-model="confirmation" class="settings-input" required autocomplete="off" autocapitalize="none" spellcheck="false" />
          <label class="lifecycle__check"><input v-model="deleteConfirmed" type="checkbox" required /> <span>{{ key('deleteConfirm') }}</span></label>
          <div class="lifecycle__actions"><button class="btn btn--sm lifecycle__delete" :disabled="!deleteConfirmed || !deletePassword || confirmation !== auth.currentUser?.username">{{ key(busy === 'delete' ? 'deleting' : 'delete') }}</button></div>
        </fieldset>
      </form>
    </details>
  </section>
</template>

<style scoped>
.lifecycle { display: grid; gap: 18px; }
.lifecycle__section { border: 1px solid var(--color-separator, var(--border-color)); border-radius: 16px; background: var(--color-bg-secondary, var(--bg-secondary)); overflow: hidden; }
summary { padding: 20px; cursor: pointer; font-weight: 600; }
summary:focus-visible { outline: 2px solid var(--color-primary, var(--primary-color)); outline-offset: -3px; border-radius: 12px; }
.lifecycle__body, .lifecycle__form { padding: 0 20px 20px; }
.lifecycle__body { display: grid; gap: 12px; }
.lifecycle__subsection { border: 1px solid var(--color-separator, var(--border-color)); border-radius: 12px; }
.lifecycle__subsection summary { font-size: var(--font-md, 15px); padding: 16px; }
.lifecycle__subsection .lifecycle__form { padding: 0 16px 16px; }
.lifecycle__hint { color: var(--color-label-secondary, var(--text-secondary)); font-size: var(--font-sm, 14px); line-height: 1.6; margin: 0 0 16px; }
fieldset { border: 0; margin: 0; padding: 0; min-width: 0; display: grid; gap: 12px; }
fieldset label { font-size: var(--font-sm, 14px); }
.settings-input { width: 100%; min-width: 0; min-height: 44px; border: 1px solid var(--color-separator, var(--border-color)); border-radius: 9px; background: var(--color-bg-tertiary, var(--bg-primary)); color: inherit; font: inherit; padding: 10px 12px; }
.settings-input:focus-visible { outline: 2px solid var(--color-primary, var(--primary-color)); outline-offset: 2px; }
.lifecycle__alias { display: grid; gap: 8px; }
.lifecycle__input-row { display: flex; gap: 10px; align-items: center; }
.lifecycle__input-row .btn { flex-shrink: 0; }
.lifecycle__actions { display: flex; gap: 10px; margin-top: 4px; flex-wrap: wrap; }
.lifecycle__check { display: flex; gap: 10px; align-items: flex-start; line-height: 1.6; margin: 5px 0; }
.lifecycle__check input { margin-top: 5px; flex-shrink: 0; }
.lifecycle__warning { padding: 14px 16px; line-height: 1.65; border-radius: 10px; background: color-mix(in srgb, var(--color-warning, #e7a20d) 12%, transparent); }
.lifecycle__error, .lifecycle__delete { color: var(--color-danger, #e5484d); }
.lifecycle__delete { border: 1px solid currentColor; }
.lifecycle__notice { line-height: 1.6; overflow-wrap: anywhere; }
.lifecycle__error { display: flex; align-items: center; flex-wrap: wrap; gap: 12px; }
@media (max-width: 600px) { .lifecycle__body, .lifecycle__form { padding-left: 12px; padding-right: 12px; } summary { padding: 16px; } }
</style>
