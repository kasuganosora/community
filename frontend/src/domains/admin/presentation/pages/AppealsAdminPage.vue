<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { appealAdminApi, type Appeal } from '../../infrastructure/appealAdminApi'
import { t } from '@/shared/i18n'

const filters = reactive({ state: 'open' })
const items = ref<Appeal[]>([])
const selected = ref<Appeal | null>(null)
const offset = ref(0)
const note = ref('')
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const limit = 40
let request = 0

const states: Record<string, string> = {
  open: t('admin.AppealsAdminPage.s6'),
  accepted: t('admin.AppealsAdminPage.s7'),
  rejected: t('admin.AppealsAdminPage.s8'),
}
function stateLabel(state: string) { return states[state] || state }
function message(e: unknown) { return e instanceof Error ? e.message : t('admin.AppealsAdminPage.s39') }
function when(value?: string) {
  if (!value) return ''
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}
function statusLabel(id: string) {
  return !id || id === '0' ? t('admin.AppealsAdminPage.s38') : `#${id}`
}

async function load(reset = false) {
  if (reset) offset.value = 0
  const version = ++request
  loading.value = true
  error.value = ''
  try {
    const result = await appealAdminApi.list({ state: filters.state, limit, offset: offset.value })
    if (version === request) items.value = result
  } catch (e) {
    if (version === request) { items.value = []; error.value = message(e) }
  } finally {
    if (version === request) loading.value = false
  }
}

function detail(appeal: Appeal) {
  selected.value = appeal
  note.value = ''
  error.value = ''
}

async function resolve(accept: boolean) {
  const appeal = selected.value
  if (!appeal || appeal.state !== 'open' || busy.value) return
  const prompt = accept
    ? t('admin.AppealsAdminPage.s31', [appeal.id])
    : t('admin.AppealsAdminPage.s32', [appeal.id])
  if (!window.confirm(prompt)) return
  busy.value = true
  error.value = ''
  try {
    selected.value = await appealAdminApi.resolve(appeal.id, accept, note.value.trim())
    note.value = ''
    await load()
  } catch (e) {
    error.value = message(e)
  } finally {
    busy.value = false
  }
}

async function page(next: boolean) {
  offset.value = next ? offset.value + limit : Math.max(0, offset.value - limit)
  selected.value = null
  await load()
}

onMounted(() => load())
</script>

<template>
  <section class="admin-page">
    <header>
      <h1>{{ $t('admin.AppealsAdminPage.s1') }}</h1>
      <p>{{ $t('admin.AppealsAdminPage.s2') }}</p>
    </header>
    <form class="admin-toolbar" :aria-label="$t('admin.AppealsAdminPage.s3')" @submit.prevent="!busy && load(true)">
      <label class="admin-field">{{ $t('admin.AppealsAdminPage.s4') }}
        <select v-model="filters.state">
          <option value="">{{ $t('admin.AppealsAdminPage.s5') }}</option>
          <option value="open">{{ $t('admin.AppealsAdminPage.s6') }}</option>
          <option value="accepted">{{ $t('admin.AppealsAdminPage.s7') }}</option>
          <option value="rejected">{{ $t('admin.AppealsAdminPage.s8') }}</option>
        </select>
      </label>
      <button class="admin-button admin-button--primary" :disabled="loading || busy">{{ $t('admin.AppealsAdminPage.s9') }}</button>
    </form>
    <p v-if="error" class="admin-error" role="alert">{{ error }}</p>
    <p v-if="loading" class="admin-empty" role="status">{{ $t('admin.AppealsAdminPage.s10') }}</p>
    <div class="admin-card admin-table-wrap" role="region" :aria-label="$t('admin.AppealsAdminPage.s11')" tabindex="0">
      <table class="admin-table">
        <thead>
          <tr>
            <th>{{ $t('admin.AppealsAdminPage.s12') }}</th>
            <th>{{ $t('admin.AppealsAdminPage.s13') }}</th>
            <th>{{ $t('admin.AppealsAdminPage.s14') }}</th>
            <th>{{ $t('admin.AppealsAdminPage.s15') }}</th>
            <th>{{ $t('admin.AppealsAdminPage.s16') }}</th>
            <th>{{ $t('admin.AppealsAdminPage.s17') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="appeal in items" :key="appeal.id">
            <td>#{{ appeal.id }}</td>
            <td>{{ appeal.account_id }}</td>
            <td>{{ statusLabel(appeal.status_id) }}</td>
            <td><span class="admin-badge">{{ stateLabel(appeal.state) }}</span></td>
            <td>{{ when(appeal.created_at) }}</td>
            <td><button class="admin-button" :disabled="loading || busy" @click="detail(appeal)">{{ $t('admin.AppealsAdminPage.s40') }}</button></td>
          </tr>
        </tbody>
      </table>
      <p v-if="!loading && !error && !items.length" class="admin-empty">{{ $t('admin.AppealsAdminPage.s18') }}</p>
    </div>
    <nav class="admin-pagination" :aria-label="$t('admin.AppealsAdminPage.s19')">
      <button class="admin-button" :disabled="loading || busy || offset === 0" @click="page(false)">{{ $t('admin.AppealsAdminPage.s20') }}</button>
      <span>{{ $t('admin.AppealsAdminPage.s21', [offset / limit + 1]) }}</span>
      <button class="admin-button" :disabled="loading || busy || items.length < limit" @click="page(true)">{{ $t('admin.AppealsAdminPage.s22') }}</button>
    </nav>
    <section v-if="selected" class="admin-card" :aria-label="$t('admin.AppealsAdminPage.s23')">
      <h2>{{ $t('admin.AppealsAdminPage.s24', [selected.id]) }} · {{ stateLabel(selected.state) }}</h2>
      <dl>
        <dt>{{ $t('admin.AppealsAdminPage.s13') }}</dt><dd>{{ selected.account_id }}</dd>
        <dt>{{ $t('admin.AppealsAdminPage.s14') }}</dt><dd>{{ statusLabel(selected.status_id) }}</dd>
        <dt>{{ $t('admin.AppealsAdminPage.s16') }}</dt><dd>{{ when(selected.created_at) }}</dd>
        <dt v-if="selected.resolved_at">{{ $t('admin.AppealsAdminPage.s35') }}</dt>
        <dd v-if="selected.resolved_at">{{ when(selected.resolved_at) }}</dd>
      </dl>
      <h3>{{ $t('admin.AppealsAdminPage.s25') }}</h3>
      <p class="appeal-text">{{ selected.text || $t('admin.AppealsAdminPage.s26') }}</p>
      <h3>{{ $t('admin.AppealsAdminPage.s33') }}</h3>
      <p class="appeal-text">{{ selected.admin_note || $t('admin.AppealsAdminPage.s34') }}</p>
      <p>{{ $t('admin.AppealsAdminPage.s36') }} <router-link to="/admin/accounts">{{ $t('admin.AppealsAdminPage.s37') }}</router-link></p>
      <form v-if="selected.state === 'open'" class="admin-toolbar" @submit.prevent>
        <label class="admin-field appeal-note">{{ $t('admin.AppealsAdminPage.s27') }}
          <textarea v-model="note" rows="3" maxlength="2000" :placeholder="$t('admin.AppealsAdminPage.s28')" />
        </label>
        <button type="button" class="admin-button admin-button--primary" :disabled="busy" @click="resolve(true)">{{ $t('admin.AppealsAdminPage.s29') }}</button>
        <button type="button" class="admin-button admin-button--danger" :disabled="busy" @click="resolve(false)">{{ $t('admin.AppealsAdminPage.s30') }}</button>
      </form>
    </section>
  </section>
</template>

<style scoped>
.appeal-text { white-space: pre-wrap; overflow-wrap: anywhere; }
.appeal-note { flex: 1 1 16rem; min-width: 0; }
.appeal-note textarea {
  width: 100%;
  min-height: 5rem;
  font: inherit;
  font-size: 1rem;
}
</style>
