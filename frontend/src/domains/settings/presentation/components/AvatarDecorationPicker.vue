<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { communityAssetsApi, type AvatarDecoration } from '@/domains/admin/infrastructure/communityAssetsApi'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { useSettingsScope } from '../composables/useSettingsScope'
const auth = useAuthStore()
const items = ref<AvatarDecoration[]>([]), selected = ref('0'), busy = ref(false), loading = ref(true), loaded = ref(false), error = ref(''), notice = ref('')
const scope = useSettingsScope(() => { items.value = []; loaded.value = false; selected.value = '0'; busy.value = false; loading.value = false; error.value = ''; notice.value = '' })
async function load() {
  const current = scope()
  loading.value = true; error.value = ''
  selected.value = auth.currentUser?.avatar_decoration_id || '0'
  try { const result = await communityAssetsApi.decorations(); if (current()) { items.value = result; loaded.value = true } }
  catch { if (current()) error.value = '无法加载头像挂件' }
  finally { if (current()) loading.value = false }
}
async function save() {
  if (busy.value || loading.value || !loaded.value) return
  const current = scope()
  busy.value = true; error.value = ''; notice.value = ''
  try {
    await communityAssetsApi.selectDecoration(selected.value)
    if (!current()) return
    await auth.fetchCurrentUser()
    if (current()) notice.value = '头像挂件已更新'
  } catch { if (current()) error.value = '保存失败，挂件可能已停用' }
  finally { if (current()) busy.value = false }
}
onMounted(load)
</script>
<template><form class="decoration-picker" @submit.prevent="save"><p>仅在本站显示，不会替换原头像。</p><p v-if="loading" role="status">正在加载挂件…</p><div v-else class="decoration-options"><label><input v-model="selected" :disabled="busy" type="radio" value="0" name="avatar-decoration" />不使用</label><label v-for="item in items" :key="item.id"><input v-model="selected" :disabled="busy" type="radio" :value="item.id" name="avatar-decoration" /><img :src="item.image_url" alt="" width="56" height="56" />{{ item.name }}</label></div><p v-if="error" role="alert">{{ error }} <button type="button" class="btn btn--ghost btn--sm" :disabled="busy || loading" @click="load">重试</button></p><p v-if="notice" role="status">{{ notice }}</p><div class="settings-actions"><button type="submit" class="btn btn--primary" :disabled="busy || loading || !loaded">{{ busy?'保存中…':'保存挂件' }}</button></div></form></template>
<style scoped>.decoration-picker{padding:20px}.decoration-options{display:flex;flex-wrap:wrap;gap:16px;margin:16px 0}.decoration-options label{display:flex;align-items:center;gap:8px}.decoration-options img{object-fit:contain}</style>
