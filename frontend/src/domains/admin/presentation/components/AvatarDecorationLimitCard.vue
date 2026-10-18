<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { AdminAlert, AdminButton, AdminField } from './ui'
import { communityAssetsApi } from '../../infrastructure/communityAssetsApi'
import { t } from '@/shared/i18n'
const emit = defineEmits<{ 'busy-change': [busy: boolean]; saved: []; cancel: [] }>()
const limit = ref<number | string>(1), saved = ref(1), max = ref(16), busy = ref(false), loading = ref(true), loaded = ref(false), error = ref('')
let scope = 0
const valid = computed(() => typeof limit.value === 'number' && Number.isInteger(limit.value) && limit.value >= 1 && limit.value <= max.value)
async function load() {
  const current = scope
  loading.value = true; error.value = ''; loaded.value = false
  try {
    const s = await communityAssetsApi.adminDecorationSettings()
    if (current !== scope) return
    limit.value = saved.value = s.limit; max.value = s.max_limit; loaded.value = true
  } catch { if (current === scope) error.value = t('admin.AvatarDecorationLimitCard.s5') }
  finally { if (current === scope) loading.value = false }
}
async function save() {
  if (busy.value || !loaded.value || !valid.value || limit.value === saved.value) return
  const current = scope
  busy.value = true; emit('busy-change', true); error.value = ''
  try {
    const s = await communityAssetsApi.saveDecorationSettings(Number(limit.value))
    if (current !== scope) return
    limit.value = saved.value = s.limit; busy.value = false; emit('busy-change', false); emit('saved')
  } catch { if (current === scope) error.value = t('admin.AvatarDecorationLimitCard.s7', [max.value]) }
  finally { if (current === scope) { busy.value = false; emit('busy-change', false) } }
}
watch(sessionEpoch, () => { scope++; loaded.value = false; busy.value = false; emit('busy-change', false) }, { flush: 'sync' })
onBeforeUnmount(() => { scope++ })
void load()
</script>
<template>
  <form class="admin-form" @submit.prevent="save">
    <p v-if="loading" role="status">{{ $t('admin.BrandAssetsAdminPage.s20') }}</p>
    <template v-if="loaded">
      <AdminField :label="$t('admin.AvatarDecorationLimitCard.s1')" :hint="$t('admin.AvatarDecorationLimitCard.s2', [max])"><template #default="{ id }"><input :id="id" v-model.number="limit" required type="number" inputmode="numeric" min="1" :max="max" step="1" :disabled="busy" /></template></AdminField>
    </template>
    <AdminAlert v-if="error" tone="danger">{{ error }}<template v-if="!loaded" #action><AdminButton size="sm" :disabled="loading" @click="load">{{ $t('admin.BrandAssetsAdminPage.s4') }}</AdminButton></template></AdminAlert>
    <div class="limit-actions"><AdminButton :disabled="busy" @click="emit('cancel')">{{ $t('admin.AvatarDecorationsAdminPage.cancel') }}</AdminButton><AdminButton type="submit" variant="primary" :loading="busy" :disabled="!loaded || !valid || limit === saved">{{ $t('admin.AvatarDecorationLimitCard.s4') }}</AdminButton></div>
  </form>
</template>
<style scoped>
.limit-actions { display: flex; justify-content: flex-end; gap: 8px; padding-top: 8px; }
</style>
