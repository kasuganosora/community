<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { authApi, type AuthorizedApp } from '@/domains/auth/infrastructure/authApi'

const apps = ref<AuthorizedApp[]>([])

async function refresh() {
  apps.value = await authApi.listAuthorizedApps()
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
    <p v-if="apps.length === 0" class="hint">还没有已授权的应用。</p>
    <ul v-else>
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
