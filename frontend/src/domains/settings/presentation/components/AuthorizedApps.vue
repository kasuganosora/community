<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useSettingsScope } from '../composables/useSettingsScope'
import { MessagePlugin } from 'tdesign-vue-next'
import { authApi, type AuthorizedApp } from '@/domains/auth/infrastructure/authApi'

const apps = ref<AuthorizedApp[]>([])
const loadError = ref('')
const fetching = ref(true)
const revoking = ref('')
const scope = useSettingsScope(() => { apps.value = []; loadError.value = ''; fetching.value = false; revoking.value = '' })

async function refresh() {
  const current = scope()
  fetching.value = true
  loadError.value = ''
  try {
    const result = await authApi.listAuthorizedApps()
    if (current()) apps.value = result
  } catch {
    if (current()) loadError.value = '加载失败，请重试'
  } finally {
    if (current()) fetching.value = false
  }
}

async function revoke(id: string) {
  if (revoking.value || fetching.value) return
  const current = scope()
  revoking.value = id
  try {
    await authApi.revokeAuthorizedApp(id)
    if (!current()) return
    apps.value = apps.value.filter(app => app.id !== id)
  } catch {
    if (current()) MessagePlugin.error('撤销失败')
  } finally {
    if (current()) revoking.value = ''
  }
}

onMounted(() => {
  refresh().catch(() => {})
})
</script>

<template>
  <div class="apps">
    <p v-if="fetching" class="hint" role="status">正在加载…</p>
    <div v-else-if="loadError" class="settings-error" role="alert">{{ loadError }} <button type="button" class="btn btn--ghost" @click="refresh">重试</button></div>
    <p v-if="!fetching && !loadError && apps.length === 0" class="hint">还没有已授权的应用。</p>
    <ul v-else-if="!fetching && !loadError && apps.length">
      <li v-for="app in apps" :key="app.id">
        <span>
          <strong>{{ app.name }}</strong>
          <small v-if="app.scopes">{{ app.scopes }}</small>
        </span>
        <button type="button" :disabled="!!revoking" :aria-busy="revoking === app.id" @click="revoke(app.id)">{{ revoking === app.id ? '撤销中…' : '撤销' }}</button>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.apps { display: flex; flex-direction: column; gap: var(--space-2); }
.hint { color: var(--color-label-secondary); font-size: var(--text-footnote); margin: 0; }
ul { list-style: none; margin: 0; padding: 0; }
li { display: flex; justify-content: space-between; gap: var(--space-3); align-items: center; min-height: var(--hit-target); }
small { display: block; color: var(--color-label-secondary); }
button { color: var(--color-danger); background: none; border: 0; cursor: pointer; }
</style>
