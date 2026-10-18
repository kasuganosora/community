<script setup lang="ts">
import { computed, onBeforeUnmount, ref, useId, watch } from 'vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { t } from '@/shared/i18n'
import { communityAssetsApi as api, type AvatarDecoration } from '../../infrastructure/communityAssetsApi'
import { AdminAlert, AdminBadge, AdminButton, AdminCheck, AdminEmpty, AdminField, AdminPageHeader } from '../components/ui'
import AvatarDecorationLimitCard from '../components/AvatarDecorationLimitCard.vue'

const items = ref<AvatarDecoration[]>([]), loading = ref(false), error = ref(''), notice = ref('')
const modal = ref<'editor' | 'settings' | null>(null), dialog = ref<HTMLDialogElement>(), titleId = useId()
const editing = ref<AvatarDecoration | null>(null), name = ref(''), enabled = ref(true)
const file = ref<File | null>(null), preview = ref(''), formError = ref(''), busy = ref(false), settingsBusy = ref(false)
const saving = computed(() => busy.value || settingsBusy.value)
const dialogTitle = computed(() => modal.value === 'settings' ? t('admin.AvatarDecorationsAdminPage.settings') : t(editing.value ? 'admin.AvatarDecorationsAdminPage.edit' : 'admin.AvatarDecorationsAdminPage.create'))
let scope = 0, request = 0
let objectURL = ''
function clearPreview() { if (objectURL) URL.revokeObjectURL(objectURL); objectURL = ''; preview.value = ''; file.value = null }
async function load() {
  const current = scope, mine = ++request
  loading.value = true; error.value = ''
  try { const rows = await api.list('avatar_decorations'); if (current === scope && mine === request) items.value = rows }
  catch { if (current === scope && mine === request) error.value = t('admin.BrandAssetsAdminPage.s30') }
  finally { if (current === scope && mine === request) loading.value = false }
}
function openEditor(item?: AvatarDecoration) {
  clearPreview(); editing.value = item ?? null; name.value = item?.name ?? ''; enabled.value = item?.enabled ?? true
  preview.value = item?.image_url ?? ''; formError.value = ''; notice.value = ''; modal.value = 'editor'
}
function close() { if (saving.value) return; dialog.value?.close(); modal.value = null; clearPreview(); formError.value = '' }
watch(modal, value => { if (value) dialog.value?.showModal() }, { flush: 'post' })
function choose(event: Event) {
  const input = event.target as HTMLInputElement, selected = input.files?.[0]
  if (!selected) return
  clearPreview(); formError.value = ''
  if (!['image/png', 'image/jpeg', 'image/gif', 'image/webp'].includes(selected.type) || selected.size > 2 * 1024 * 1024 || selected.size === 0) {
    formError.value = t('admin.AvatarDecorationsAdminPage.invalidImage'); input.value = ''; return
  }
  file.value = selected; objectURL = URL.createObjectURL(selected); preview.value = objectURL
}
async function mutate(action: () => Promise<unknown>, failure: string) {
  if (saving.value) return
  const current = scope
  busy.value = true; formError.value = ''; notice.value = ''
  try {
    await action()
    if (current !== scope) return
    busy.value = false; close(); notice.value = t('admin.BrandAssetsAdminPage.s33'); void load()
  } catch { if (current === scope) formError.value = failure }
  finally { if (current === scope) busy.value = false }
}
function save() {
  if (!name.value.trim() || (!editing.value && !file.value)) return
  return mutate(() => {
    if (editing.value) return api.update('avatar_decorations', editing.value.id, { name: name.value.trim(), enabled: enabled.value })
    const data = new FormData(); data.append('name', name.value.trim()); data.append('enabled', String(enabled.value)); data.append('file', file.value!)
    return api.create('avatar_decorations', data)
  }, t('admin.AvatarDecorationsAdminPage.saveFailed'))
}
function remove() {
  if (!editing.value || saving.value || !window.confirm(t('admin.AvatarDecorationsAdminPage.confirmDelete'))) return
  const id = editing.value.id
  return mutate(() => api.remove('avatar_decorations', id), t('admin.BrandAssetsAdminPage.s37'))
}
function settingsSaved() { close(); notice.value = t('admin.BrandAssetsAdminPage.s33') }
watch(sessionEpoch, () => { scope++; request++; busy.value = false; settingsBusy.value = false; close(); items.value = []; error.value = ''; notice.value = ''; loading.value = false }, { flush: 'sync' })
onBeforeUnmount(() => { scope++; clearPreview() })
void load()
</script>

<template>
  <section class="admin-page decorations-page">
    <AdminPageHeader :title="$t('admin.AvatarDecorationsAdminPage.title')">
      <AdminButton variant="primary" @click="openEditor()"><t-icon name="add" aria-hidden="true" />{{ $t('admin.BrandAssetsAdminPage.s6') }}</AdminButton>
      <AdminButton @click="modal = 'settings'"><t-icon name="setting" aria-hidden="true" />{{ $t('admin.AvatarDecorationsAdminPage.settings') }}</AdminButton>
    </AdminPageHeader>
    <AdminAlert v-if="error" tone="danger">{{ error }}<template #action><AdminButton size="sm" :disabled="loading" @click="load">{{ $t('admin.BrandAssetsAdminPage.s4') }}</AdminButton></template></AdminAlert>
    <AdminAlert v-if="notice" tone="success">{{ notice }}</AdminAlert>
    <p v-if="loading" role="status" class="admin-hint">{{ $t('admin.BrandAssetsAdminPage.s20') }}</p>
    <div v-if="items.length" class="decoration-grid" :aria-busy="loading">
      <button v-for="item in items" :key="item.id" type="button" class="decoration-tile" :aria-label="$t('admin.AvatarDecorationsAdminPage.editName', [item.name])" @click="openEditor(item)">
        <span class="decoration-tile__preview checkerboard"><img :src="item.image_url" alt="" loading="lazy" /></span>
        <span class="decoration-tile__info"><strong>{{ item.name }}</strong><AdminBadge :tone="item.enabled ? 'success' : 'neutral'">{{ $t(item.enabled ? 'admin.BrandAssetsAdminPage.s21' : 'admin.BrandAssetsAdminPage.s22') }}</AdminBadge></span>
      </button>
    </div>
    <AdminEmpty v-else-if="!loading && !error"><t-icon name="image" size="36" aria-hidden="true" /><strong>{{ $t('admin.BrandAssetsAdminPage.s27', [$t('admin.BrandAssetsAdminPage.s29')]) }}</strong><span>{{ $t('admin.AvatarDecorationsAdminPage.empty') }}</span></AdminEmpty>

    <dialog v-if="modal" ref="dialog" class="decoration-dialog" :aria-labelledby="titleId" @cancel.prevent="close" @keydown.esc.stop>
      <header class="decoration-dialog__header"><h2 :id="titleId">{{ dialogTitle }}</h2><AdminButton :aria-label="$t('admin.AvatarDecorationsAdminPage.close')" :disabled="saving" @click="close"><t-icon name="close" aria-hidden="true" /></AdminButton></header>
      <div class="decoration-dialog__body">
        <AvatarDecorationLimitCard v-if="modal === 'settings'" @busy-change="settingsBusy = $event" @saved="settingsSaved" @cancel="close" />
        <form v-else class="admin-form" @submit.prevent="save">
          <div class="decoration-editor__image checkerboard"><img v-if="preview" :src="preview" :alt="$t('admin.AvatarDecorationsAdminPage.preview')" /><t-icon v-else name="image-add" size="44" aria-hidden="true" /></div>
          <AdminField v-if="!editing" :label="$t('admin.BrandAssetsAdminPage.s8')" :hint="$t('admin.BrandAssetsAdminPage.s10')"><template #default="{ id }"><input :id="id" type="file" accept="image/png,image/jpeg,image/gif,image/webp" :disabled="busy" required @change="choose" /></template></AdminField>
          <AdminField :label="$t('admin.BrandAssetsAdminPage.s7')"><template #default="{ id }"><input :id="id" v-model="name" required maxlength="80" :disabled="busy" autofocus /></template></AdminField>
          <AdminCheck v-model="enabled" :disabled="busy" :label="$t('admin.BrandAssetsAdminPage.s16')" />
          <AdminAlert v-if="formError" tone="danger">{{ formError }}</AdminAlert>
          <div class="decoration-dialog__actions">
            <AdminButton v-if="editing" variant="danger" :disabled="busy" @click="remove">{{ $t('admin.BrandAssetsAdminPage.s26') }}</AdminButton>
            <span class="decoration-dialog__spacer" />
            <AdminButton :disabled="busy" @click="close">{{ $t('admin.AvatarDecorationsAdminPage.cancel') }}</AdminButton>
            <AdminButton type="submit" variant="primary" :loading="busy" :disabled="!name.trim() || (!editing && !file)">{{ $t('admin.BrandAssetsAdminPage.s18') }}</AdminButton>
          </div>
        </form>
      </div>
    </dialog>
  </section>
</template>

<style scoped>
.decoration-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(210px, 100%), 1fr)); gap: 20px; }
.decoration-tile { display: grid; padding: 0; min-width: 0; overflow: hidden; text-align: start; border: 1px solid var(--color-separator); border-radius: 14px; background: var(--color-bg-row); color: var(--color-label); transition: border-color .15s, box-shadow .15s; }
.decoration-tile:hover { border-color: var(--admin-accent); box-shadow: 0 4px 16px rgb(0 0 0 / 8%); }
.checkerboard { background-color: var(--color-bg-row); background-image: conic-gradient(var(--color-fill) 25%, transparent 0 50%, var(--color-fill) 0 75%, transparent 0); background-size: 20px 20px; }
.decoration-tile__preview { display: grid; place-items: center; aspect-ratio: 1.3; padding: 24px; }
.decoration-tile__preview img { width: 100%; height: 132px; object-fit: contain; }
.decoration-tile__info { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 16px; border-top: 1px solid var(--color-separator); }
.decoration-tile__info strong { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 600; }
.decoration-tile__info :deep(.admin-badge) { flex-shrink: 0; }
.admin-empty { display: grid; justify-items: center; gap: 12px; padding-block: 80px; }
.decoration-dialog { width: min(520px, calc(100vw - 32px)); max-height: calc(100dvh - 48px); margin: auto; padding: 0; border: 1px solid var(--color-separator); border-radius: 16px; background: var(--color-bg-elevated); color: var(--color-label); box-shadow: var(--shadow-popover); }
.decoration-dialog[open] { display: flex; flex-direction: column; }
.decoration-dialog::backdrop { background: rgb(0 0 0 / 55%); }
.decoration-dialog__header { display: flex; justify-content: space-between; align-items: center; gap: 16px; padding: 16px 24px; border-bottom: 1px solid var(--color-separator); }
.decoration-dialog__header h2 { margin: 0; font-size: 18px; }
.decoration-dialog__header button { padding: 8px; }
.decoration-dialog__body { padding: 24px; overflow-y: auto; overscroll-behavior: contain; }
.decoration-editor__image { display: grid; place-items: center; height: 200px; border-radius: 12px; color: var(--color-label-tertiary); }
.decoration-editor__image img { width: 100%; height: 100%; max-height: 200px; object-fit: contain; padding: 16px; }
.decoration-dialog__actions { display: flex; flex-wrap: wrap; gap: 8px; padding-top: 8px; }
.decoration-dialog__spacer { flex: 1; }
</style>
