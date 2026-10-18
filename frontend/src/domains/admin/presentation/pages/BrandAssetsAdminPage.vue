<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { AdminAlert, AdminBadge, AdminButton, AdminCheck, AdminEmpty, AdminField, AdminPageHeader } from '../components/ui'
import AvatarDecorationsAdminPage from './AvatarDecorationsAdminPage.vue'
import { communityAssetsApi, type Advertisement, type AvatarDecoration } from '../../infrastructure/communityAssetsApi'
import { t } from '@/shared/i18n'
const props = defineProps<{ kind: 'avatar_decorations' | 'advertisements' }>()
const ads = computed(() => props.kind === 'advertisements'), heading = computed(() => ads.value ? t('admin.BrandAssetsAdminPage.s28') : t('admin.BrandAssetsAdminPage.s29'))
const items = ref<(AvatarDecoration & Advertisement)[]>([]), name = ref(''), target = ref(''), start = ref(''), end = ref(''), enabled = ref(false), file = ref<File | null>(null), busy = ref(false), loading = ref(false), error = ref(''), notice = ref(''), editing = ref('')
let generation = 0
async function load() {
  const version = ++generation
  loading.value = true; error.value = ''
  try { const result = await communityAssetsApi.list(props.kind); if (version === generation) items.value = result }
  catch { if (version === generation) error.value = t('admin.BrandAssetsAdminPage.s30') }
  finally { if (version === generation) loading.value = false }
}
function choose(e: Event) { file.value = (e.target as HTMLInputElement).files?.[0] ?? null }
function localDate(value: string | null) { if (!value) return ''; const date = new Date(value); return new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 16) }
function edit(item: AvatarDecoration & Advertisement) { editing.value = item.id; name.value = ads.value ? item.title : item.name; target.value = item.target_url || ''; enabled.value = item.enabled; start.value = localDate(item.starts_at); end.value = localDate(item.ends_at); error.value = '' }
function reset() { editing.value = ''; name.value = ''; target.value = ''; start.value = ''; end.value = ''; enabled.value = false; file.value = null }
function iso(value: string) { return value ? new Date(value).toISOString() : '' }
async function mutate(request: () => Promise<unknown>, failure: string, onSuccess?: () => void) {
  if (busy.value || loading.value) return
  const version = generation
  busy.value = true; error.value = ''; notice.value = ''
  try {
    await request()
    if (version !== generation) return
    onSuccess?.()
    busy.value = false
    await load()
  } catch (e) { if (version === generation) error.value = e instanceof Error ? e.message : failure }
  finally { if (version === generation) busy.value = false }
}
function save() { return mutate(async () => {
  const values = ads.value ? { title: name.value, target_url: target.value, enabled: enabled.value, starts_at: iso(start.value), ends_at: iso(end.value) } : { name: name.value, enabled: enabled.value }
  if (editing.value) await communityAssetsApi.update(props.kind, editing.value, values)
  else {
    if (!file.value) throw new Error(t('admin.BrandAssetsAdminPage.s31'))
    const data = new FormData(); data.append('file', file.value)
    for (const [key, value] of Object.entries(values)) data.append(key, String(value))
    await communityAssetsApi.create(props.kind, data)
  }
}, t('admin.BrandAssetsAdminPage.s32'), () => { reset(); notice.value = t('admin.BrandAssetsAdminPage.s33') }) }
function toggle(item: AvatarDecoration & Advertisement) { return mutate(() => communityAssetsApi.update(props.kind, item.id, { enabled: !item.enabled }), t('admin.BrandAssetsAdminPage.s34')) }
function remove(item: AvatarDecoration & Advertisement) {
  if (busy.value || loading.value || !window.confirm(ads.value ? t('admin.BrandAssetsAdminPage.s35') : t('admin.BrandAssetsAdminPage.s36'))) return
  return mutate(() => communityAssetsApi.remove(props.kind, item.id), t('admin.BrandAssetsAdminPage.s37'))
}
watch(() => props.kind, () => { items.value = []; busy.value = false; notice.value = ''; reset(); if (ads.value) void load(); else { generation++; loading.value = false } }, { immediate: true })
watch(sessionEpoch, () => { generation++; items.value = []; busy.value = false; loading.value = false; error.value = ''; notice.value = ''; reset() }, { flush: 'sync' })
onBeforeUnmount(() => { generation++ })
</script>
<template>
  <AvatarDecorationsAdminPage v-if="!ads" />
  <section v-else class="admin-page">
    <AdminPageHeader :title="$t('admin.BrandAssetsAdminPage.s1', [heading])" :description="ads?$t('admin.BrandAssetsAdminPage.s2'):$t('admin.BrandAssetsAdminPage.s3')" />
    <AdminAlert v-if="error" tone="danger">{{ error }}<template #action><AdminButton size="sm" :disabled="busy || loading" @click="load">{{ $t('admin.BrandAssetsAdminPage.s4') }}</AdminButton></template></AdminAlert>
    <AdminAlert v-if="notice" tone="success">{{ notice }}</AdminAlert>
    <form class="admin-card" @submit.prevent="save">
      <h2>{{ editing?$t('admin.BrandAssetsAdminPage.s5'):$t('admin.BrandAssetsAdminPage.s6') }}{{ heading }}</h2>
      <div class="admin-form">
        <AdminField :label="$t('admin.BrandAssetsAdminPage.s7')"><template #default="{ id }"><input :id="id" v-model="name" required :maxlength="ads?120:80" /></template></AdminField>
        <AdminField v-if="!editing" :label="$t('admin.BrandAssetsAdminPage.s8')" :hint="ads ? $t('admin.BrandAssetsAdminPage.s9') : $t('admin.BrandAssetsAdminPage.s10')"><template #default="{ id }"><input :id="id" type="file" accept="image/png,image/jpeg,image/gif,image/webp" required @change="choose" /></template></AdminField>
        <template v-if="ads">
          <AdminField :label="$t('admin.BrandAssetsAdminPage.s11')"><template #default="{ id }"><input :id="id" v-model="target" type="url" placeholder="https://…" required /></template></AdminField>
          <AdminField :label="$t('admin.BrandAssetsAdminPage.s12')" :hint="$t('admin.BrandAssetsAdminPage.s13')"><template #default="{ id }"><input :id="id" v-model="start" type="datetime-local" /></template></AdminField>
          <AdminField :label="$t('admin.BrandAssetsAdminPage.s14')" :hint="$t('admin.BrandAssetsAdminPage.s15')"><template #default="{ id }"><input :id="id" v-model="end" type="datetime-local" /></template></AdminField>
        </template>
        <AdminCheck v-model="enabled" :label="$t('admin.BrandAssetsAdminPage.s16')" />
      </div>
      <div class="admin-form-actions">
        <AdminButton type="submit" variant="primary" :loading="busy" :disabled="loading">{{ busy?$t('admin.BrandAssetsAdminPage.s17'):$t('admin.BrandAssetsAdminPage.s18') }}</AdminButton>
        <AdminButton v-if="editing" :disabled="busy || loading" @click="reset">{{ $t('admin.BrandAssetsAdminPage.s19') }}</AdminButton>
      </div>
    </form>
    <p v-if="loading" class="admin-empty" role="status">{{ $t('admin.BrandAssetsAdminPage.s20') }}</p>
    <div class="asset-grid" :aria-busy="loading">
      <article v-for="item in items" :key="item.id" class="admin-card">
        <img :src="item.image_url" :alt="ads?item.title:item.name" class="asset-preview" loading="lazy" />
        <div class="admin-card__header"><h2>{{ ads?item.title:item.name }}</h2><AdminBadge :tone="item.enabled ? 'success' : 'neutral'">{{ item.enabled?$t('admin.BrandAssetsAdminPage.s21'):$t('admin.BrandAssetsAdminPage.s22') }}</AdminBadge></div>
        <template v-if="ads"><p>{{ item.target_url }}</p><p>{{ item.starts_at?new Date(item.starts_at).toLocaleString():$t('admin.BrandAssetsAdminPage.s23') }} — {{ item.ends_at?new Date(item.ends_at).toLocaleString():$t('admin.BrandAssetsAdminPage.s24') }}</p></template>
        <div class="admin-actions">
          <AdminButton size="sm" :disabled="busy || loading" @click="edit(item)">{{ $t('admin.BrandAssetsAdminPage.s5') }}</AdminButton>
          <AdminButton size="sm" :disabled="busy || loading" @click="toggle(item)">{{ item.enabled?$t('admin.BrandAssetsAdminPage.s25'):$t('admin.BrandAssetsAdminPage.s16') }}</AdminButton>
          <AdminButton size="sm" variant="danger" :disabled="busy || loading" @click="remove(item)">{{ $t('admin.BrandAssetsAdminPage.s26') }}</AdminButton>
        </div>
      </article>
    </div>
    <AdminEmpty v-if="!loading && !error && !items.length">{{ $t('admin.BrandAssetsAdminPage.s27', [heading]) }}</AdminEmpty>
  </section>
</template>
<style scoped>
.asset-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(240px, 100%), 1fr)); gap: 16px; }
.asset-grid > * { min-width: 0; }
.asset-preview { width: 100%; height: 150px; object-fit: contain; border-radius: 10px; background: var(--color-bg-grouped); }
.asset-grid p { overflow-wrap: anywhere; }
</style>
