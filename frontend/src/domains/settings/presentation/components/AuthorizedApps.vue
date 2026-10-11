<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { authApi, type AuthorizedApp } from '@/domains/auth/infrastructure/authApi'

const apps = ref<AuthorizedApp[]>([])
const loadError = ref('')
const fetching = ref(true)

async function refresh() {
  fetching.value = true
  loadError.value = ''
  try {
    apps.value = await authApi.listAuthorizedApps()
  } catch {
    loadError.value = '加载失败，请重试'
  } finally {
    fetching.value = false
  }
}

async function revoke(id: string) {
  try {
    await authApi.revokeAuthorizedApp(id)
    await refresh()
  } catch {
    MessagePlugin.error('撤销失败')
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
        <button type="button" @click="revoke(app.id)">撤销</button>
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
