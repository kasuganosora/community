<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { AdminButton, AdminField } from './ui'
import { communityAssetsApi } from '../../infrastructure/communityAssetsApi'
const limit = ref(1), saved = ref(1), max = ref(16), busy = ref(false), message = ref(''), failed = ref(false)
onMounted(async () => {
  try { const s = await communityAssetsApi.adminDecorationSettings(); limit.value = saved.value = s.limit; max.value = s.max_limit } catch { failed.value = true; message.value = '无法读取挂件数量上限' }
})
async function save() {
  if (busy.value) return
  busy.value = true; message.value = ''; failed.value = false
  try { const s = await communityAssetsApi.saveDecorationSettings(Math.round(limit.value)); limit.value = saved.value = s.limit; message.value = '已保存' }
  catch { failed.value = true; message.value = `保存失败，请输入 1–${max.value}` }
  finally { busy.value = false }
}
</script>
<template>
  <form class="admin-card" @submit.prevent="save">
    <h2>每人可佩戴数量</h2>
    <p class="admin-hint">与 Misskey 的 avatarDecorationLimit 一致，默认 1，最多 {{ max }}。降低上限不会修改已有选择，用户下次保存时生效。</p>
    <AdminField label="数量上限"><template #default="{ id }"><input :id="id" v-model.number="limit" type="number" inputmode="numeric" min="1" :max="max" step="1" :disabled="busy" /></template></AdminField>
    <p v-if="message" :role="failed ? 'alert' : 'status'">{{ message }}</p>
    <div class="admin-form-actions"><AdminButton type="submit" variant="primary" :loading="busy" :disabled="busy || limit === saved">保存</AdminButton></div>
  </form>
</template>
