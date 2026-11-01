<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import BrandMark from '@/shared/components/common/BrandMark.vue'
import LanguagePicker from '@/shared/components/common/LanguagePicker.vue'
import { appealApi, type Appeal, type AppealContext } from '../../infrastructure/appealApi'
import { clearAppealSession } from '@/shared/api/appealSession'
import { useAuthStore } from '../stores/authStore'
import { t } from '@/shared/i18n'

const router = useRouter()
const authStore = useAuthStore()
const appeals = ref<Appeal[]>([])
const text = ref('')
const suspension = ref<AppealContext['suspension']>(null)
let request = 0
const loading = ref(true)
const busy = ref(false)
const error = ref('')
const ready = ref(false)

const states: Record<string, string> = {
  open: t('auth.AppealPage.s11'),
  accepted: t('auth.AppealPage.s12'),
  rejected: t('auth.AppealPage.s13'),
}
const openAppeal = computed(() => appeals.value.find(appeal => appeal.state === 'open') ?? null)
const history = computed(() => appeals.value.filter(appeal => appeal !== openAppeal.value))

function stateLabel(state: string) { return states[state] || state }
function message(e: unknown) { return e instanceof Error ? e.message : t('auth.AppealPage.s7') }
function when(value?: string) {
  if (!value) return ''
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}
function statusLabel(id: string) {
  return !id || id === '0' ? t('auth.AppealPage.s24') : t('auth.AppealPage.s23', [id])
}

async function load(clearError = true) {
  const version = ++request
  loading.value = true
  if (clearError) error.value = ''
  try {
    const result = await appealApi.context()
    if (version !== request) return
    if (!result.suspended) {
      clearAppealSession()
      await authStore.fetchCurrentUser()
      if (version === request) await router.replace({ name: 'home' })
      return
    }
    appeals.value = result.items
    suspension.value = result.suspension
    ready.value = true
  } catch (e) {
    if (version === request && !error.value) error.value = message(e)
  } finally {
    if (version === request) loading.value = false
  }
}

async function submit() {
  if (busy.value || loading.value || !suspension.value?.can_appeal) return
  const body = text.value.trim()
  if (!body) {
    error.value = t('auth.AppealPage.s21')
    return
  }
  busy.value = true
  error.value = ''
  try {
    const created = await appealApi.create(body, suspension.value.status_id)
    text.value = ''
    suspension.value = { ...suspension.value, can_appeal: false }
    appeals.value = [created, ...appeals.value.filter(appeal => appeal.id !== created.id)]
  } catch (e) {
    error.value = message(e)
    await load(false)
  } finally {
    busy.value = false
  }
}

async function signOut() {
  await authStore.logout(true, false)
}

onMounted(() => load())
onBeforeUnmount(() => { request++ })
</script>

<template>
  <main class="appeal-page">
    <section class="appeal-card">
      <div class="appeal-logo"><BrandMark :size="40" /></div>
      <h1>{{ $t('auth.AppealPage.s1') }}</h1>
      <p class="appeal-lead">{{ $t('auth.AppealPage.s2') }}</p>
      <p v-if="loading" role="status" aria-live="polite">{{ $t('auth.AppealPage.s7') }}</p>
      <p v-if="error" class="appeal-error" role="alert">{{ error }}</p>
      <button type="button" class="btn btn--ghost" :disabled="loading || busy" @click="load()">{{ $t('auth.AppealPage.refresh') }}</button>
      <p v-if="ready && !suspension" class="appeal-hint">{{ $t('auth.AppealPage.unavailable') }}</p>
      <p v-else-if="ready && suspension && !suspension.can_appeal && !openAppeal" class="appeal-hint">{{ $t('auth.AppealPage.alreadyReviewed') }}</p>

      <article v-if="openAppeal" class="appeal-case" :aria-label="$t('auth.AppealPage.s9')">
        <h2>{{ $t('auth.AppealPage.s9') }}</h2>
        <p><span class="appeal-label">{{ $t('auth.AppealPage.s10') }}</span> {{ stateLabel(openAppeal.state) }}</p>
        <p class="appeal-body">{{ openAppeal.text }}</p>
        <p class="appeal-meta">{{ statusLabel(openAppeal.status_id) }} · {{ when(openAppeal.created_at) }}</p>
      </article>

      <form v-else-if="ready && suspension?.can_appeal" class="appeal-form" @submit.prevent="submit">
        <p v-if="!appeals.length" class="appeal-hint">{{ $t('auth.AppealPage.s20') }}</p>
        <p class="appeal-hint">{{ $t('auth.AppealPage.s18') }}</p>
        <label class="appeal-field">{{ $t('auth.AppealPage.s3') }}
          <textarea v-model="text" rows="6" maxlength="5000" :disabled="busy" required :placeholder="$t('auth.AppealPage.s4')" />
        </label>
        <button class="btn btn--primary appeal-submit" type="submit" :disabled="busy || loading">{{ busy ? $t('auth.AppealPage.s8') : $t('auth.AppealPage.s6') }}</button>
      </form>

      <section v-if="history.length" class="appeal-history">
        <h2>{{ $t('auth.AppealPage.s22') }}</h2>
        <article v-for="appeal in history" :key="appeal.id" class="appeal-case">
          <p><span class="appeal-label">{{ $t('auth.AppealPage.s10') }}</span> {{ stateLabel(appeal.state) }}</p>
          <p class="appeal-body">{{ appeal.text }}</p>
          <p class="appeal-meta">{{ statusLabel(appeal.status_id) }} · {{ $t('auth.AppealPage.s14') }} {{ when(appeal.created_at) }}</p>
          <p v-if="appeal.resolved_at" class="appeal-meta">{{ $t('auth.AppealPage.s15') }} {{ when(appeal.resolved_at) }}</p>
          <p v-if="appeal.admin_note" class="appeal-body"><span class="appeal-label">{{ $t('auth.AppealPage.s16') }}</span> {{ appeal.admin_note }}</p>
          <p v-else-if="appeal.state !== 'open'" class="appeal-meta">{{ $t('auth.AppealPage.s17') }}</p>
        </article>
      </section>

      <button type="button" class="btn btn--ghost appeal-signout" :disabled="busy" @click="signOut">{{ $t('auth.AppealPage.s19') }}</button>
      <div class="appeal-lang"><LanguagePicker compact /></div>
    </section>
  </main>
</template>

<style scoped>
.appeal-page {
  min-height: 100dvh;
  display: flex;
  justify-content: center;
  padding: calc(var(--space-6) + var(--safe-top)) var(--gutter) calc(var(--space-6) + var(--safe-bottom));
  background: var(--color-bg-grouped);
  color: var(--color-label);
}
.appeal-card {
  width: 100%;
  max-width: 32rem;
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}
.appeal-logo { display: flex; justify-content: center; }
.appeal-card h1 {
  margin: 0;
  text-align: center;
  font-size: var(--text-title-2);
  line-height: var(--leading-title-2);
}
.appeal-lead, .appeal-hint, .appeal-meta { margin: 0; color: var(--color-label-secondary); overflow-wrap: anywhere; }
.appeal-error { margin: 0; color: var(--color-danger); overflow-wrap: anywhere; }
.appeal-form, .appeal-case, .appeal-history { display: flex; flex-direction: column; gap: var(--space-3); }
.appeal-field { display: flex; flex-direction: column; gap: var(--space-1); font-size: var(--text-footnote); }
.appeal-field textarea, .appeal-field input {
  width: 100%;
  box-sizing: border-box;
  min-height: 44px;
  padding: var(--space-3);
  border: 1px solid var(--color-separator);
  border-radius: var(--radius-md, 10px);
  background: var(--color-bg-row);
  color: inherit;
  font: inherit;
  font-size: 16px;
}
.appeal-field textarea { min-height: 8rem; resize: vertical; }
.appeal-submit, .appeal-signout { width: 100%; }
.appeal-body { margin: 0; white-space: pre-wrap; overflow-wrap: anywhere; }
.appeal-label { font-weight: var(--weight-semibold); }
.appeal-case {
  padding: var(--space-4);
  border-radius: var(--radius-xl);
  background: var(--color-bg-row);
}
.appeal-case h2, .appeal-history h2 { margin: 0; font-size: var(--text-headline); }
.appeal-lang { display: flex; justify-content: center; }
</style>
