<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { authApi, type PasskeyItem } from '@/domains/auth/infrastructure/authApi'
import { createPasskey, passkeySupported } from '@/shared/utils/webauthn'

const supported = passkeySupported()
const items = ref<PasskeyItem[]>([])
const loadError = ref('')
const fetching = ref(true)
const loading = ref(false)
const name = ref('')

async function refresh() {
  fetching.value = true
  loadError.value = ''
  try {
    items.value = await authApi.listPasskeys()
  } catch {
    loadError.value = '加载失败，请重试'
  } finally {
    fetching.value = false
  }
}

async function add() {
  loading.value = true
  try {
    const options = await authApi.passkeyRegisterBegin(name.value.trim() || 'Passkey')
    const credential = await createPasskey(options)
    await authApi.passkeyRegisterFinish(credential)
    name.value = ''
    await refresh()
    MessagePlugin.success('通行密钥已添加')
  } catch (e) {
    MessagePlugin.error(e instanceof Error ? e.message : '添加通行密钥失败')
  } finally {
    loading.value = false
  }
}

async function remove(id: string) {
  try {
    await authApi.deletePasskey(id)
    await refresh()
  } catch {
    MessagePlugin.error('删除失败')
  }
}

onMounted(() => {
  refresh().catch(() => {})
})
</script>

<template>
  <div class="passkeys">
    <p v-if="fetching" class="hint" role="status">正在加载…</p>
    <div v-else-if="loadError" class="settings-error" role="alert">{{ loadError }} <button type="button" class="btn btn--ghost" @click="refresh">重试</button></div>
    <p v-if="!supported" class="hint">这台设备或浏览器不支持通行密钥。</p>
    <template v-else>
      <form class="settings-form" @submit.prevent="add">
        <div class="settings-field">
          <label for="passkey-name" class="settings-field__label">通行密钥名称</label>
          <input id="passkey-name" v-model="name" class="settings-input" placeholder="例如：这台电脑" autocomplete="off" />
        </div>
        <div class="settings-actions"><button type="submit" class="btn btn--primary" :disabled="loading">{{ loading ? '添加中…' : '添加通行密钥' }}</button></div>
      </form>
      <p v-if="!fetching && !loadError && items.length === 0" class="hint">还没有通行密钥。添加后可以用它代替密码登录。</p>
      <ul v-else-if="!fetching && !loadError && items.length">
        <li v-for="item in items" :key="item.id">
          <span>{{ item.name }}</span>
          <button type="button" class="passkey-remove" @click="remove(item.id)">删除</button>
        </li>
      </ul>
    </template>
  </div>
</template>

<style scoped>
.passkeys { display: flex; flex-direction: column; gap: var(--space-3); }
.add { display: flex; gap: var(--space-2); flex-wrap: wrap; }
.add :deep(.t-input) { flex: 1; min-width: 10rem; }
.hint { color: var(--color-label-secondary); font-size: var(--text-footnote); margin: 0; }
ul { list-style: none; margin: 0; padding: 0; }
li { display: flex; justify-content: space-between; align-items: center; min-height: var(--hit-target); }
.passkey-remove { color: var(--color-danger); background: none; border: 0; cursor: pointer; }
</style>
