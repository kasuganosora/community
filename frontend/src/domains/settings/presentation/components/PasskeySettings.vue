<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { authApi, type PasskeyItem } from '@/domains/auth/infrastructure/authApi'
import { createPasskey, passkeySupported } from '@/shared/utils/webauthn'

const supported = passkeySupported()
const items = ref<PasskeyItem[]>([])
const loading = ref(false)
const name = ref('')

async function refresh() {
  items.value = await authApi.listPasskeys()
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
    <p v-if="!supported" class="hint">这台设备或浏览器不支持通行密钥。</p>
    <template v-else>
      <div class="add">
        <t-input v-model="name" placeholder="名称，例如 这台电脑" />
        <t-button theme="primary" :loading="loading" @click="add">添加通行密钥</t-button>
      </div>
      <p v-if="items.length === 0" class="hint">还没有通行密钥。添加后可以用它代替密码登录。</p>
      <ul v-else>
        <li v-for="item in items" :key="item.id">
          <span>{{ item.name }}</span>
          <button type="button" @click="remove(item.id)">删除</button>
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
button { color: var(--color-danger); background: none; border: 0; cursor: pointer; }
</style>
