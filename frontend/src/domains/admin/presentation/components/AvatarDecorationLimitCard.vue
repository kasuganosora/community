<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { AdminButton, AdminField } from './ui'
import { communityAssetsApi } from '../../infrastructure/communityAssetsApi'
import { t } from '@/shared/i18n'
const limit = ref(1), saved = ref(1), max = ref(16), busy = ref(false), message = ref(''), failed = ref(false)
onMounted(async () => {
  try { const s = await communityAssetsApi.adminDecorationSettings(); limit.value = saved.value = s.limit; max.value = s.max_limit } catch { failed.value = true; message.value = t('admin.AvatarDecorationLimitCard.s5') }
})
async function save() {
  if (busy.value) return
  busy.value = true; message.value = ''; failed.value = false
  try { const s = await communityAssetsApi.saveDecorationSettings(Math.round(limit.value)); limit.value = saved.value = s.limit; message.value = t('admin.AvatarDecorationLimitCard.s6') }
  catch { failed.value = true; message.value = t('admin.AvatarDecorationLimitCard.s7', [max.value]) }
  finally { busy.value = false }
}
</script>
<template>
  <form class="admin-card" @submit.prevent="save">
    <h2>{{ $t('admin.AvatarDecorationLimitCard.s1') }}</h2>
    <p class="admin-hint">{{ $t('admin.AvatarDecorationLimitCard.s2', [max]) }}</p>
    <AdminField :label="$t('admin.AvatarDecorationLimitCard.s3')"><template #default="{ id }"><input :id="id" v-model.number="limit" type="number" inputmode="numeric" min="1" :max="max" step="1" :disabled="busy" /></template></AdminField>
    <p v-if="message" :role="failed ? 'alert' : 'status'">{{ message }}</p>
    <div class="admin-form-actions"><AdminButton type="submit" variant="primary" :loading="busy" :disabled="busy || limit === saved">{{ $t('admin.AvatarDecorationLimitCard.s4') }}</AdminButton></div>
  </form>
</template>
