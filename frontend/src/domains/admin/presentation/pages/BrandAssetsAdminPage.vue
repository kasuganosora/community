<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { AdminAlert, AdminBadge, AdminButton, AdminCheck, AdminEmpty, AdminField, AdminPageHeader } from '../components/ui'
import AvatarDecorationLimitCard from '../components/AvatarDecorationLimitCard.vue'
import { communityAssetsApi, type Advertisement, type AvatarDecoration } from '../../infrastructure/communityAssetsApi'
const props = defineProps<{ kind: 'avatar_decorations' | 'advertisements' }>()
const ads = computed(() => props.kind === 'advertisements'), heading = computed(() => ads.value ? '广告' : '头像挂件')
const items = ref<(AvatarDecoration & Advertisement)[]>([]), name = ref(''), target = ref(''), start = ref(''), end = ref(''), enabled = ref(false), file = ref<File | null>(null), busy = ref(false), loading = ref(false), error = ref(''), notice = ref(''), editing = ref('')
let generation = 0
async function load() {
  const version = ++generation
  loading.value = true; error.value = ''
  try { const result = await communityAssetsApi.list(props.kind); if (version === generation) items.value = result }
  catch { if (version === generation) error.value = '加载失败，请重试' }
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
    if (!file.value) throw new Error('请选择图片')
    const data = new FormData(); data.append('file', file.value)
    for (const [key, value] of Object.entries(values)) data.append(key, String(value))
    await communityAssetsApi.create(props.kind, data)
  }
}, '保存失败，请检查图片、链接和时间', () => { reset(); notice.value = '已保存' }) }
function toggle(item: AvatarDecoration & Advertisement) { return mutate(() => communityAssetsApi.update(props.kind, item.id, { enabled: !item.enabled }), '状态更新失败') }
function remove(item: AvatarDecoration & Advertisement) {
  if (busy.value || loading.value || !window.confirm(ads.value ? '删除此广告？展示会立即停止。' : '删除此挂件？所有使用者将恢复普通头像。')) return
  return mutate(() => communityAssetsApi.remove(props.kind, item.id), '删除失败')
}
watch(() => props.kind, () => { items.value = []; busy.value = false; notice.value = ''; reset(); void load() }, { immediate: true })
watch(sessionEpoch, () => { generation++; items.value = []; busy.value = false; loading.value = false; error.value = ''; notice.value = ''; reset() }, { flush: 'sync' })
onBeforeUnmount(() => { generation++ })
</script>
<template>
  <section class="admin-page">
    <AdminPageHeader :title="`${heading}管理`" :description="ads?'图片广告在社区右侧展示，支持定时上下线。':'成员可在个人设置中选用，叠加显示在头像上。'" />
    <AdminAlert v-if="error" tone="danger">{{ error }}<template #action><AdminButton size="sm" :disabled="busy || loading" @click="load">重试</AdminButton></template></AdminAlert>
    <AdminAlert v-if="notice" tone="success">{{ notice }}</AdminAlert>
    <form class="admin-card" @submit.prevent="save">
      <h2>{{ editing?'编辑':'新增' }}{{ heading }}</h2>
      <div class="admin-form">
        <AdminField label="名称"><template #default="{ id }"><input :id="id" v-model="name" required :maxlength="ads?120:80" /></template></AdminField>
        <AdminField v-if="!editing" label="图片" :hint="ads ? 'PNG / JPEG / GIF / WebP，最多 2 MiB' : '透明 PNG 效果最佳，最多 2 MiB'"><template #default="{ id }"><input :id="id" type="file" accept="image/png,image/jpeg,image/gif,image/webp" required @change="choose" /></template></AdminField>
        <template v-if="ads">
          <AdminField label="目标链接"><template #default="{ id }"><input :id="id" v-model="target" type="url" placeholder="https://…" required /></template></AdminField>
          <AdminField label="开始时间" hint="留空表示立即"><template #default="{ id }"><input :id="id" v-model="start" type="datetime-local" /></template></AdminField>
          <AdminField label="结束时间" hint="留空表示不限"><template #default="{ id }"><input :id="id" v-model="end" type="datetime-local" /></template></AdminField>
        </template>
        <AdminCheck v-model="enabled" label="启用" />
      </div>
      <div class="admin-form-actions">
        <AdminButton type="submit" variant="primary" :loading="busy" :disabled="loading">{{ busy?'处理中…':'保存' }}</AdminButton>
        <AdminButton v-if="editing" :disabled="busy || loading" @click="reset">取消编辑</AdminButton>
      </div>
    </form>
    <p v-if="loading" class="admin-empty" role="status">正在加载…</p>
    <div class="asset-grid" :aria-busy="loading">
      <article v-for="item in items" :key="item.id" class="admin-card">
        <img :src="item.image_url" :alt="ads?item.title:item.name" class="asset-preview" loading="lazy" />
        <div class="admin-card__header"><h2>{{ ads?item.title:item.name }}</h2><AdminBadge :tone="item.enabled ? 'success' : 'neutral'">{{ item.enabled?'已启用':'已停用' }}</AdminBadge></div>
        <template v-if="ads"><p>{{ item.target_url }}</p><p>{{ item.starts_at?new Date(item.starts_at).toLocaleString():'立即' }} — {{ item.ends_at?new Date(item.ends_at).toLocaleString():'不限' }}</p></template>
        <div class="admin-actions">
          <AdminButton size="sm" :disabled="busy || loading" @click="edit(item)">编辑</AdminButton>
          <AdminButton size="sm" :disabled="busy || loading" @click="toggle(item)">{{ item.enabled?'停用':'启用' }}</AdminButton>
          <AdminButton size="sm" variant="danger" :disabled="busy || loading" @click="remove(item)">删除</AdminButton>
        </div>
      </article>
    </div>
    <AdminEmpty v-if="!loading && !error && !items.length">暂无{{ heading }}</AdminEmpty>
    <AvatarDecorationLimitCard v-if="!ads" />
  </section>
</template>
<style scoped>
.asset-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(240px, 100%), 1fr)); gap: 16px; }
.asset-grid > * { min-width: 0; }
.asset-preview { width: 100%; height: 150px; object-fit: contain; border-radius: 10px; background: var(--color-bg-grouped); }
.asset-grid p { overflow-wrap: anywhere; }
</style>
