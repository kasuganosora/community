<script setup lang="ts">
import { AdminAlert, AdminBadge, AdminButton, AdminCheck, AdminEmpty, AdminField, AdminPageHeader } from '../components/ui'
import { onMounted, reactive, ref } from 'vue'
import { announcementsAuditApi as api, type Announcement } from '../../infrastructure/announcementsAuditApi'
import { t } from '@/shared/i18n'
const rows = ref<Announcement[]>([])
const published = ref('')
const cursors = ref([''])
const selected = ref<string>()
const draft = reactive({ content: '', all_day: false, scheduled_at: '', starts_at: '', ends_at: '' })
const busy = ref(false)
const error = ref('')
function localTime(value: string | null) { if (!value) return ''; const d = new Date(value); return new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16) }
function edit(row?: Announcement) {
  selected.value = row?.id
  Object.assign(draft, { content: row?.content || '', all_day: row?.all_day || false, scheduled_at: localTime(row?.scheduled_at || null), starts_at: localTime(row?.starts_at || null), ends_at: localTime(row?.ends_at || null) })
}
async function fetchRows() { rows.value = await api.list({ published: published.value || undefined, max_id: cursors.value.at(-1) || undefined }) }
async function run(work: () => Promise<void>) {
  if (busy.value) return
  busy.value = true; error.value = ''
  try { await work() } catch (e) { error.value = e instanceof Error ? e.message : t('admin.AnnouncementsAdminPage.s32') } finally { busy.value = false }
}
function load(reset = false) { if (reset) cursors.value = ['']; return run(fetchRows) }
function save() { return run(async () => { const input = { ...draft, scheduled_at: draft.scheduled_at ? new Date(draft.scheduled_at).toISOString() : null, starts_at: draft.starts_at ? new Date(draft.starts_at).toISOString() : null, ends_at: draft.ends_at ? new Date(draft.ends_at).toISOString() : null }; await api.save(input, selected.value); edit(); cursors.value = ['']; await fetchRows() }) }
function toggle(row: Announcement) { if (!window.confirm(t('admin.AnnouncementsAdminPage.s33', [row.published ? t('admin.AnnouncementsAdminPage.s25') : t('admin.AnnouncementsAdminPage.s26'), row.id]))) return; return run(async () => { await api.publish(row.id, !row.published); await fetchRows() }) }
function remove(row: Announcement) { if (!window.confirm(t('admin.AnnouncementsAdminPage.s34', [row.id]))) return; return run(async () => { await api.remove(row.id); if (selected.value === row.id) edit(); await fetchRows() }) }
function page(next: boolean) { if (next) cursors.value.push(rows.value.at(-1)?.id || ''); else cursors.value.pop(); return load() }
onMounted(() => load())
</script>
<template>
  <section class="admin-page">
    <AdminPageHeader :title="$t('admin.AnnouncementsAdminPage.s1')" :description="$t('admin.AnnouncementsAdminPage.s2')" />
    <AdminAlert v-if="error" tone="danger">{{ error }}</AdminAlert>
    <form class="admin-card" @submit.prevent="save">
      <h2>{{ selected ? $t('admin.AnnouncementsAdminPage.s3', [selected]) : $t('admin.AnnouncementsAdminPage.s4') }}</h2>
      <AdminField :label="$t('admin.AnnouncementsAdminPage.s5')"><template #default="{ id }"><textarea :id="id" v-model="draft.content" required maxlength="10000" rows="6" :disabled="busy" /></template></AdminField>
      <div class="admin-toolbar">
        <AdminField :label="$t('admin.AnnouncementsAdminPage.s6')"><template #default="{ id }"><input :id="id" v-model="draft.scheduled_at" type="datetime-local" :disabled="busy" /></template></AdminField>
        <AdminField :label="$t('admin.AnnouncementsAdminPage.s7')"><template #default="{ id }"><input :id="id" v-model="draft.starts_at" type="datetime-local" :disabled="busy" /></template></AdminField>
        <AdminField :label="$t('admin.AnnouncementsAdminPage.s8')"><template #default="{ id }"><input :id="id" v-model="draft.ends_at" type="datetime-local" :disabled="busy" /></template></AdminField>
      </div>
      <AdminCheck v-model="draft.all_day" :label="$t('admin.AnnouncementsAdminPage.s9')" :disabled="busy" />
      <div class="admin-form-actions">
        <AdminButton type="submit" variant="primary" :loading="busy">{{ selected ? $t('admin.AnnouncementsAdminPage.s10') : $t('admin.AnnouncementsAdminPage.s11') }}</AdminButton>
        <AdminButton v-if="selected" :disabled="busy" @click="edit()">{{ $t('admin.AnnouncementsAdminPage.s12') }}</AdminButton>
      </div>
    </form>
    <form class="admin-toolbar" @submit.prevent="load(true)">
      <AdminField :label="$t('admin.AnnouncementsAdminPage.s13')"><template #default="{ id }"><select :id="id" v-model="published"><option value="">{{ $t('admin.AnnouncementsAdminPage.s14') }}</option><option value="false">{{ $t('admin.AnnouncementsAdminPage.s15') }}</option><option value="true">{{ $t('admin.AnnouncementsAdminPage.s16') }}</option></select></template></AdminField>
      <AdminButton type="submit" :disabled="busy">{{ $t('admin.AnnouncementsAdminPage.s17') }}</AdminButton>
    </form>
    <p v-if="busy" class="admin-empty" role="status">{{ $t('admin.AnnouncementsAdminPage.s18') }}</p>
    <article v-for="row in rows" :key="row.id" class="admin-card">
      <div class="admin-card__header"><h2>{{ $t('admin.AnnouncementsAdminPage.s19', [row.id]) }}</h2><AdminBadge :tone="row.published ? 'success' : 'neutral'">{{ row.published ? $t('admin.AnnouncementsAdminPage.s20') : $t('admin.AnnouncementsAdminPage.s15') }}</AdminBadge></div>
      <p class="announcement-content">{{ row.content }}</p>
      <p>{{ $t('admin.AnnouncementsAdminPage.s23', [row.scheduled_at ? new Date(row.scheduled_at).toLocaleString() : $t('admin.AnnouncementsAdminPage.s21'), row.starts_at ? new Date(row.starts_at).toLocaleString() : $t('admin.AnnouncementsAdminPage.s22'), row.ends_at ? new Date(row.ends_at).toLocaleString() : $t('admin.AnnouncementsAdminPage.s22')]) }}</p>
      <div class="admin-actions">
        <AdminButton size="sm" :disabled="busy" @click="edit(row)">{{ $t('admin.AnnouncementsAdminPage.s24') }}</AdminButton>
        <AdminButton size="sm" :disabled="busy" @click="toggle(row)">{{ row.published ? $t('admin.AnnouncementsAdminPage.s25') : $t('admin.AnnouncementsAdminPage.s26') }}</AdminButton>
        <AdminButton size="sm" variant="danger" :disabled="busy" @click="remove(row)">{{ $t('admin.AnnouncementsAdminPage.s27') }}</AdminButton>
      </div>
    </article>
    <AdminEmpty v-if="!busy && !error && !rows.length">{{ $t('admin.AnnouncementsAdminPage.s28') }}</AdminEmpty>
    <nav class="admin-pagination" :aria-label="$t('admin.AnnouncementsAdminPage.s29')">
      <AdminButton size="sm" :disabled="busy || cursors.length === 1" @click="page(false)">{{ $t('admin.AnnouncementsAdminPage.s30') }}</AdminButton>
      <AdminButton size="sm" :disabled="busy || rows.length < 40" @click="page(true)">{{ $t('admin.AnnouncementsAdminPage.s31') }}</AdminButton>
    </nav>
  </section>
</template>
<style scoped>
.announcement-content { white-space: pre-wrap; overflow-wrap: anywhere; }
</style>
