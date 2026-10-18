<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref, watch } from 'vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { discoveryApi, type DiscoveryFeedback, type ReadingPreference } from '../../infrastructure/discoveryApi'
import { invalidateDiscovery, loadDiscoveryPreference } from '../composables/discoveryState'

import { t } from '@/shared/i18n'
const preference = ref<ReadingPreference>('generic'), feedback = ref<DiscoveryFeedback[]>([])
const languages = ref<string[]>([]), languageInput = ref(''), languageError = ref('')
const loading = ref(true), ready = ref(false), enabled = ref(true), busy = ref(false), error = ref(''), saved = ref('')
let version = 0
const labels = { hide_status: t('discovery.DiscoverySettings.s30'), hide_author: t('discovery.DiscoverySettings.s31'), hide_tag: t('discovery.DiscoverySettings.s32'), dismiss_suggestion: t('discovery.DiscoverySettings.s33') }
const commonLanguages = [
  ['zh', t('discovery.DiscoverySettings.s34')], ['zh-Hans', t('discovery.DiscoverySettings.s35')], ['zh-Hant', t('discovery.DiscoverySettings.s36')], ['en', 'English'], ['ja', t('discovery.DiscoverySettings.s37')],
  ['ko', '한국어'], ['fr', 'Français'], ['de', 'Deutsch'], ['es', 'Español'], ['pt', 'Português'], ['ru', 'Русский'], ['ar', 'العربية'],
] as const
function languageName(code: string) { return commonLanguages.find(([value]) => value === code)?.[1] || code }
function addLanguage(code = languageInput.value) {
  languageError.value = ''
  const value = code.trim()
  if (!value) return
  if (languages.value.length >= 4) { languageError.value = t('discovery.DiscoverySettings.s38'); return }
  try {
    const canonical = Intl.getCanonicalLocales(value)[0]
    if (!canonical) throw new Error('invalid language')
    if (!languages.value.includes(canonical)) languages.value = [...languages.value, canonical]
    languageInput.value = ''
  } catch { languageError.value = t('discovery.DiscoverySettings.s39') }
}
function selectLanguage(event: Event) {
  const select = event.target as HTMLSelectElement
  addLanguage(select.value); select.value = ''
}
async function load() {
  const mine = ++version, epoch = sessionEpoch.value
  loading.value = true; ready.value = false; error.value = ''; feedback.value = []
  try {
    const [settings, rows] = await Promise.all([loadDiscoveryPreference(true), discoveryApi.feedback()])
    if (mine !== version || epoch !== sessionEpoch.value || !settings) return
    preference.value = settings.preference; languages.value = settings.languages || []; languageError.value = ''; languageInput.value = ''; enabled.value = settings.enabled !== false; feedback.value = rows; ready.value = true
  } catch (e) { if (mine === version) error.value = e instanceof Error ? e.message : t('discovery.DiscoverySettings.s40') }
  finally { if (mine === version) loading.value = false }
}
async function save() {
  if (busy.value || !ready.value) return
  languageError.value = ''
  if (languageInput.value.trim()) addLanguage()
  if (languageError.value) return
  busy.value = true; error.value = ''; saved.value = ''
  const mine = version, epoch = sessionEpoch.value
  try {
    const value = await discoveryApi.setPreference(preference.value, languages.value)
    if (mine !== version || epoch !== sessionEpoch.value) return
    preference.value = value.preference; invalidateDiscovery(value); saved.value = t('discovery.DiscoverySettings.s41')
  } catch (e) { if (mine === version) error.value = e instanceof Error ? e.message : t('discovery.DiscoverySettings.s42') }
  finally { if (mine === version) busy.value = false }
}
async function undo(item: DiscoveryFeedback) {
  if (busy.value) return
  busy.value = true; error.value = ''; saved.value = ''
  const mine = version, epoch = sessionEpoch.value
  try {
    const value = await discoveryApi.removeFeedback(item.id)
    if (mine !== version || epoch !== sessionEpoch.value) return
    feedback.value = feedback.value.filter(row => row.id !== item.id)
    invalidateDiscovery(value); saved.value = t('discovery.DiscoverySettings.s43')
  } catch (e) { if (mine === version) error.value = e instanceof Error ? e.message : t('discovery.DiscoverySettings.s44') }
  finally { if (mine === version) busy.value = false }
}
watch(sessionEpoch, () => { busy.value = false; saved.value = ''; void load() }, { flush: 'sync' })
onMounted(load)
onBeforeUnmount(() => { ++version })
</script>

<template>
  <div class="discovery-settings" :aria-busy="loading || busy">
    <p v-if="loading" role="status">{{ $t('discovery.DiscoverySettings.s1') }}</p>
    <form v-else class="settings-form" @submit.prevent="save">
      <p v-if="!enabled" class="hint">{{ $t('discovery.DiscoverySettings.s2') }}</p>
      <fieldset :disabled="busy || !ready" class="discovery-options">
        <legend class="settings-field__label">{{ $t('discovery.DiscoverySettings.s3') }}</legend>
        <label class="settings-check"><input v-model="preference" type="radio" value="off" name="discovery-mode" /><span><strong>{{ $t('discovery.DiscoverySettings.s4') }}</strong><small>{{ $t('discovery.DiscoverySettings.s5') }}</small></span></label>
        <label class="settings-check"><input v-model="preference" type="radio" value="generic" name="discovery-mode" /><span><strong>{{ $t('discovery.DiscoverySettings.s6') }}</strong><small>{{ $t('discovery.DiscoverySettings.s7') }}</small></span></label>
        <label class="settings-check"><input v-model="preference" type="radio" value="personalized" name="discovery-mode" /><span><strong>{{ $t('discovery.DiscoverySettings.s8') }}</strong><small>{{ $t('discovery.DiscoverySettings.s9') }}</small></span></label>
      </fieldset>
      <fieldset :disabled="busy || !ready" class="discovery-options" aria-describedby="discovery-language-hint">
        <legend class="settings-field__label">{{ $t('discovery.DiscoverySettings.s10') }}</legend>
        <p id="discovery-language-hint" class="hint">{{ $t('discovery.DiscoverySettings.s11') }}</p>
        <div class="discovery-languages">
          <label class="settings-field"><span class="settings-field__label">{{ $t('discovery.DiscoverySettings.s12') }}</span><select class="settings-input" :aria-label="$t('discovery.DiscoverySettings.s13')" :disabled="languages.length >= 4" @change="selectLanguage"><option value="">{{ $t('discovery.DiscoverySettings.s14') }}</option><option v-for="[code, name] in commonLanguages" :key="code" :value="code" :disabled="languages.includes(code)">{{ name }}</option></select></label>
          <label class="settings-field"><span class="settings-field__label">{{ $t('discovery.DiscoverySettings.s15') }}</span><span class="discovery-language-input"><input v-model="languageInput" class="settings-input" :placeholder="$t('discovery.DiscoverySettings.s16')" maxlength="35" :disabled="languages.length >= 4" @keydown.enter.prevent="addLanguage()" /><button type="button" class="btn btn--secondary btn--sm" :disabled="!languageInput.trim() || languages.length >= 4" @click="addLanguage()">{{ $t('discovery.DiscoverySettings.s17') }}</button></span></label>
        </div>
        <ul v-if="languages.length" class="discovery-language-values"><li v-for="code in languages" :key="code"><button type="button" class="btn btn--secondary btn--sm" :aria-label="$t('discovery.DiscoverySettings.s18', [languageName(code)])" @click="languages = languages.filter(item => item !== code); languageError = ''">{{ languageName(code) }} <span aria-hidden="true">×</span></button></li></ul>
        <p v-if="languageError" class="settings-error" role="alert">{{ languageError }}</p>
      </fieldset>
      <p class="hint">{{ $t('discovery.DiscoverySettings.s19') }}</p>
      <div class="settings-actions"><button type="submit" class="btn btn--primary btn--sm" :disabled="busy || !ready">{{ busy ? $t('discovery.DiscoverySettings.s20') : $t('discovery.DiscoverySettings.s21') }}</button></div>
    </form>
    <p v-if="error" class="settings-error" role="alert">{{ error }} <button type="button" class="btn btn--ghost btn--sm" :disabled="busy" @click="load">{{ $t('discovery.DiscoverySettings.s22') }}</button></p>
    <p v-if="saved" class="hint" role="status">{{ saved }}</p>
    <section v-if="!loading" class="discovery-rules" aria-labelledby="discovery-rules-title">
      <h3 id="discovery-rules-title">{{ $t('discovery.DiscoverySettings.s23') }}</h3>
      <p class="hint">{{ $t('discovery.DiscoverySettings.s24') }}</p>
      <p v-if="!feedback.length && !error" class="hint">{{ $t('discovery.DiscoverySettings.s25') }}</p>
      <ul v-else>
        <li v-for="item in feedback" :key="item.id"><span><strong>{{ labels[item.action] || $t('discovery.DiscoverySettings.s26') }}</strong><small>{{ item.target_label || (item.target_kind === 'account' ? $t('discovery.DiscoverySettings.accountUnavailable') : `${item.target_kind === 'tag' ? '#' : $t('discovery.DiscoverySettings.s28')}${item.target_key}`) }}</small><small v-if="item.created_at">{{ new Date(item.created_at).toLocaleDateString() }}</small></span><button type="button" class="btn btn--secondary btn--sm" :disabled="busy" @click="undo(item)">{{ $t('discovery.DiscoverySettings.s29') }}</button></li>
      </ul>
    </section>
  </div>
</template>

<style scoped>
.discovery-settings { display: flex; flex-direction: column; gap: 1rem; }
.discovery-options { border: 0; margin: 0; padding: 0; display: grid; gap: .75rem; }
.discovery-options legend { margin-bottom: .75rem; }
.discovery-options .settings-check { align-items: flex-start; }
.discovery-options input { margin-top: .25rem; }
.discovery-options strong, .discovery-options small, .discovery-rules strong, .discovery-rules small { display: block; }
.discovery-options small, .discovery-rules small { color: var(--color-label-secondary); line-height: 1.6; font-size: .8125rem; margin-top: .25rem; overflow-wrap: anywhere; }
.discovery-rules { border-top: 1px solid var(--color-separator); padding-top: 1rem; }
.discovery-rules h3 { font-size: 1rem; margin-bottom: .5rem; }
.discovery-rules ul { list-style: none; padding: 0; }
.discovery-rules li { display: flex; align-items: center; justify-content: space-between; gap: 1rem; padding: .75rem 0; border-bottom: 1px solid var(--color-separator); }
.discovery-rules li > span { min-width: 0; }
.discovery-rules button { flex-shrink: 0; }
.discovery-languages { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: .75rem; }
.discovery-language-input { display: flex; align-items: center; gap: .5rem; min-width: 0; }
.discovery-language-input input { min-width: 0; }
.discovery-language-input button { flex: none; }
.discovery-language-values { display: flex; flex-wrap: wrap; gap: .5rem; list-style: none; padding: 0; }
@media (max-width: 699px) { .discovery-languages { grid-template-columns: minmax(0, 1fr); } }
</style>
