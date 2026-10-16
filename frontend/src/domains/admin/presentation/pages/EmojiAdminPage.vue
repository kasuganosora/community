<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, reactive, ref } from 'vue'
import { emojiAdminApi, type AdminEmoji, type EmojiFields } from '../../infrastructure/emojiAdminApi'

import { t } from '@/shared/i18n'
const origin = ref<'local' | 'remote'>('local')
const q = ref(''), category = ref(''), domain = ref('')
const page = ref(1), total = ref(0), limit = 40
const items = ref<AdminEmoji[]>([]), selected = ref<string[]>([])
const loading = ref(false), busy = ref(false), error = ref(''), notice = ref('')
const editorElement = ref<HTMLFormElement>()
const editor = ref(false), editing = ref<AdminEmoji | null>(null)
const copySource = ref<AdminEmoji | null>(null), copyName = ref('')
const imageId = ref(''), preview = ref(''), aliases = ref(''), uploadMeta = ref(''), previewNote = ref('')
const batchAction = ref('disable'), batchText = ref(''), batchValue = ref(true)
const roles = ref<{ id: string; name: string }[]>([]), rolesError = ref('')
const form = reactive<EmojiFields>(defaults())
let objectUrl = '', request = 0, uploadVersion = 0
function defaults(): EmojiFields {
  return { reaction_role_ids: [], shortcode: '', aliases: [], category: '', license: '', local_only: false, is_sensitive: false, visible_in_picker: true, disabled: false }
}
const pages = computed(() => Math.max(1, Math.ceil(total.value / limit)))
const allSelected = computed(() => items.value.length > 0 && items.value.every(e => selected.value.includes(e.id)))
const actionLabel: Record<string, string> = {
  enable: t('admin.EmojiAdminPage.s64'), disable: t('admin.EmojiAdminPage.s65'), delete: t('admin.EmojiAdminPage.s66'), set_category: t('admin.EmojiAdminPage.s67'), set_license: t('admin.EmojiAdminPage.s68'),
  set_aliases: t('admin.EmojiAdminPage.s69'), add_aliases: t('admin.EmojiAdminPage.s70'), remove_aliases: t('admin.EmojiAdminPage.s71'), set_local_only: t('admin.EmojiAdminPage.s72'), set_sensitive: t('admin.EmojiAdminPage.s73'),
}
function message(e: unknown) { return e instanceof Error ? e.message : t('admin.EmojiAdminPage.s95') }
function formatSize(size: number) {
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
  return `${(size / (1024 * 1024)).toFixed(2)} MB`
}
function revokePreview() { if (objectUrl) URL.revokeObjectURL(objectUrl); objectUrl = '' }
async function load() {
  const version = ++request
  loading.value = true; error.value = ''; selected.value = []
  try {
    const result = await emojiAdminApi.list({ origin: origin.value, q: q.value, category: category.value, domain: domain.value, page: page.value, limit })
    if (version !== request) return
    items.value = result.items; total.value = result.total
  } catch (e) { if (version === request) { error.value = message(e); items.value = [] } }
  finally { if (version === request) loading.value = false }
}
function search() { page.value = 1; void load() }
function changeOrigin() { batchAction.value = 'disable'; search() }
function toggleAll() { selected.value = allSelected.value ? [] : items.value.map(e => e.id) }
function openEditor(emoji: AdminEmoji | null = null) {
  revokePreview(); uploadVersion++; editing.value = emoji; imageId.value = ''; uploadMeta.value = ''; previewNote.value = ''; error.value = ''; notice.value = ''
  const initial = defaults()
  for (const key of Object.keys(initial) as (keyof EmojiFields)[]) {
    Object.assign(form, { [key]: emoji?.[key] ?? initial[key] })
  }
  aliases.value = (emoji?.aliases || []).join(', '); preview.value = emoji?.url || ''; editor.value = true
  void nextTick(() => { editorElement.value?.scrollIntoView?.({ block: 'start', behavior: 'instant' }); editorElement.value?.querySelector<HTMLInputElement>('input:not([type=file])')?.focus({ preventScroll: true }) })
}
function closeEditor() { if (busy.value) return; uploadVersion++; editor.value = false; revokePreview() }
async function upload(event: Event) {
  const input = event.target as HTMLInputElement, file = input.files?.[0]
  if (!file) return
  input.value = ''
  const allowed = ['image/png', 'image/apng', 'image/jpeg', 'image/gif', 'image/webp']
  if (!allowed.includes(file.type)) {
    error.value = t('admin.EmojiAdminPage.s98', [file.name || t('admin.EmojiAdminPage.s96'), file.type || t('admin.EmojiAdminPage.s97')])
    return
  }
  if (file.size > 2 * 1024 * 1024) {
    error.value = t('admin.EmojiAdminPage.s99', [file.name, formatSize(file.size)])
    return
  }
  const version = ++uploadVersion
  busy.value = true; error.value = ''; previewNote.value = t('admin.EmojiAdminPage.s100')
  try {
    const result = await emojiAdminApi.upload(file)
    if (version !== uploadVersion) return
    imageId.value = result.id; revokePreview(); objectUrl = URL.createObjectURL(file); preview.value = objectUrl
    uploadMeta.value = `${file.name} · ${formatSize(file.size)} · ${file.type}`
    previewNote.value = t('admin.EmojiAdminPage.s101')
  } catch (e) { error.value = t('admin.EmojiAdminPage.s102', [message(e)]); previewNote.value = '' }
  finally { busy.value = false }
}
function onPreviewLoad(event: Event) {
  const img = event.target as HTMLImageElement
  if (img.naturalWidth > 1024 || img.naturalHeight > 1024) {
    previewNote.value = t('admin.EmojiAdminPage.s103', [img.naturalWidth, img.naturalHeight])
  }
}
function onPreviewError() {
  previewNote.value = t('admin.EmojiAdminPage.s104')
}
function splitAliases(value: string) { return [...new Set(value.split(/[,，\n]/).map(s => s.trim()).filter(Boolean))] }
async function save() {
  if (busy.value) return
  if (!/^[a-zA-Z0-9_]{1,36}$/.test(form.shortcode)) { error.value = t('admin.EmojiAdminPage.s105'); return }
  if (!editing.value && !imageId.value) { error.value = t('admin.EmojiAdminPage.s106'); return }
  busy.value = true; error.value = ''
  try {
    const fields = editing.value?.domain
      ? { disabled: form.disabled, visible_in_picker: form.visible_in_picker }
      : { ...form, aliases: splitAliases(aliases.value), ...(imageId.value ? { image_id: imageId.value } : {}) }
    await emojiAdminApi.save(editing.value?.id || null, fields)
    editor.value = false; revokePreview(); notice.value = t('admin.EmojiAdminPage.s107'); await load()
  } catch (e) { error.value = message(e) }
  finally { busy.value = false }
}
async function remove(emoji: AdminEmoji) {
  if (!window.confirm(t('admin.EmojiAdminPage.s108', [emoji.shortcode]))) return
  busy.value = true; error.value = ''
  try { await emojiAdminApi.remove(emoji.id); notice.value = t('admin.EmojiAdminPage.s109'); await load() }
  catch (e) { error.value = message(e) } finally { busy.value = false }
}
async function copy() {
  if (!copySource.value || busy.value) return
  if (!/^[a-zA-Z0-9_]{1,36}$/.test(copyName.value)) { error.value = t('admin.EmojiAdminPage.s110'); return }
  busy.value = true; error.value = ''
  try { await emojiAdminApi.copy(copySource.value.id, copyName.value); copySource.value = null; notice.value = t('admin.EmojiAdminPage.s111'); await load() }
  catch (e) { error.value = message(e) } finally { busy.value = false }
}
async function batch() {
  if (!selected.value.length || busy.value) return
  if (batchAction.value === 'delete' && !window.confirm(t('admin.EmojiAdminPage.s112', [selected.value.length]))) return
  busy.value = true; error.value = ''
  const action = batchAction.value
  const label = actionLabel[action] || t('admin.EmojiAdminPage.s113')
  const chosen = selected.value.length
  const fields = action === 'set_category' ? { category: batchText.value }
    : action === 'set_license' ? { license: batchText.value }
    : action.includes('aliases') ? { aliases: splitAliases(batchText.value) }
    : { value: batchValue.value }
  try {
    const result = await emojiAdminApi.bulk(selected.value, action, fields)
    notice.value = result.updated < chosen
      ? t('admin.EmojiAdminPage.s114', [label, result.updated, chosen])
      : t('admin.EmojiAdminPage.s115', [label, result.updated])
    await load()
  }
  catch (e) { error.value = t('admin.EmojiAdminPage.s116', [label, message(e)]) } finally { busy.value = false }
}
async function loadRoles() {
  rolesError.value = ''
  try { roles.value = await emojiAdminApi.roles() }
  catch (e) { rolesError.value = message(e) }
}
onMounted(() => { void load(); void loadRoles() })
onUnmounted(() => { request++; uploadVersion++; revokePreview() })
</script>

<template>
  <div class="admin-page emoji-admin">
    <header class="emoji-heading">
      <div><p class="emoji-eyebrow">{{ $t('admin.EmojiAdminPage.s1') }}</p><h1>{{ $t('admin.EmojiAdminPage.s2') }}</h1><p class="muted">{{ $t('admin.EmojiAdminPage.s3') }}</p></div>
      <button type="button" class="admin-button admin-button--primary" :disabled="busy" @click="openEditor()"><t-icon name="add" aria-hidden="true" />{{ $t('admin.EmojiAdminPage.s4') }}</button>
    </header>

    <form class="admin-card emoji-search" :aria-label="$t('admin.EmojiAdminPage.s5')" @submit.prevent="search">
      <label>{{ $t('admin.EmojiAdminPage.s6') }}<select v-model="origin" :disabled="busy" @change="changeOrigin"><option value="local">{{ $t('admin.EmojiAdminPage.s7') }}</option><option value="remote">{{ $t('admin.EmojiAdminPage.s8') }}</option></select></label>
      <label class="emoji-search__query">{{ $t('admin.EmojiAdminPage.s9') }}<input v-model="q" type="search" :placeholder="$t('admin.EmojiAdminPage.s10')" :disabled="busy" /></label>
      <label>{{ $t('admin.EmojiAdminPage.s11') }}<input v-model="category" :placeholder="$t('admin.EmojiAdminPage.s12')" :disabled="busy" /></label>
      <label v-if="origin === 'remote'">{{ $t('admin.EmojiAdminPage.s13') }}<input v-model="domain" placeholder="example.org" :disabled="busy" /></label>
      <button class="admin-button" :disabled="busy || loading"><t-icon name="search" aria-hidden="true" />{{ $t('admin.EmojiAdminPage.s9') }}</button>
    </form>
    <p v-if="error" role="alert" class="admin-error emoji-feedback">{{ error }}</p>
    <p v-if="notice" role="status" class="emoji-feedback emoji-feedback--success"><t-icon name="check-circle" aria-hidden="true" />{{ notice }}</p>

    <form v-if="editor" ref="editorElement" class="admin-card editor" :aria-label="$t('admin.EmojiAdminPage.s14')" @submit.prevent="save">
      <div class="emoji-section-heading"><div><h2>{{ editing ? $t('admin.EmojiAdminPage.s15') : $t('admin.EmojiAdminPage.s4') }}</h2><p class="muted">{{ editing?.domain ? $t('admin.EmojiAdminPage.s16') : $t('admin.EmojiAdminPage.s17') }}</p></div><button type="button" class="admin-button" :disabled="busy" :aria-label="$t('admin.EmojiAdminPage.s18')" @click="closeEditor"><t-icon name="close" aria-hidden="true" /></button></div>
      <div class="editor-grid">
        <div class="editor-artwork">
          <div class="preview-stage"><img v-if="preview" :src="preview" :alt="$t('admin.EmojiAdminPage.s19')" class="preview" @load="onPreviewLoad" @error="onPreviewError" /><div v-else class="preview-empty"><t-icon name="image-add" :size="36" aria-hidden="true" /><span>{{ $t('admin.EmojiAdminPage.s20') }}</span></div></div>
          <label v-if="!editing?.domain" class="upload-label"><span class="upload-button"><t-icon name="image-add" aria-hidden="true" />{{ preview ? $t('admin.EmojiAdminPage.s21') : $t('admin.EmojiAdminPage.s22') }}</span><input type="file" accept="image/png,image/apng,image/jpeg,image/gif,image/webp" :disabled="busy" @change="upload" /></label>
          <p v-if="uploadMeta" class="muted">{{ uploadMeta }}</p><p v-if="previewNote" class="muted" role="status">{{ previewNote }}</p>
          <p v-if="!editing?.domain" class="muted upload-hint">{{ $t('admin.EmojiAdminPage.s23') }}<br />{{ $t('admin.EmojiAdminPage.s24') }}</p>
        </div>
        <div class="editor-fields">
          <template v-if="!editing?.domain">
            <label>{{ $t('admin.EmojiAdminPage.s25') }}<input v-model="form.shortcode" required pattern="[a-zA-Z0-9_]+" maxlength="36" :disabled="busy" :placeholder="$t('admin.EmojiAdminPage.s26')" /><small class="muted">{{ $t('admin.EmojiAdminPage.s27', [form.shortcode || 'shortcode']) }}</small></label>
            <p v-if="editing" class="muted">{{ $t('admin.EmojiAdminPage.s28') }}</p>
            <label>{{ $t('admin.EmojiAdminPage.s29') }}<input v-model="aliases" :placeholder="$t('admin.EmojiAdminPage.s30')" :disabled="busy" /><small class="muted">{{ $t('admin.EmojiAdminPage.s31') }}</small></label>
            <div class="editor-fields__pair"><label>{{ $t('admin.EmojiAdminPage.s11') }}<input v-model="form.category" :placeholder="$t('admin.EmojiAdminPage.s32')" :disabled="busy" /></label><label>{{ $t('admin.EmojiAdminPage.s33') }}<input v-model="form.license" :placeholder="$t('admin.EmojiAdminPage.s34')" :disabled="busy" /></label></div>
            <details class="role-options" :open="!!rolesError || form.reaction_role_ids.length > 0"><summary>{{ $t('admin.EmojiAdminPage.s35') }} <span class="muted">{{ form.reaction_role_ids.length ? $t('admin.EmojiAdminPage.s36', [form.reaction_role_ids.length]) : $t('admin.EmojiAdminPage.s37') }}</span></summary><p class="muted">{{ $t('admin.EmojiAdminPage.s38') }}</p><p v-if="rolesError" role="alert">{{ rolesError }} <button type="button" class="admin-button" :disabled="busy" @click="loadRoles">{{ $t('admin.EmojiAdminPage.s39') }}</button></p><label v-for="role in roles" :key="role.id" class="check"><input v-model="form.reaction_role_ids" type="checkbox" :value="role.id" :disabled="busy" />{{ role.name }}</label><p v-if="form.reaction_role_ids.some(id => !roles.some(role => role.id === id))" class="muted">{{ $t('admin.EmojiAdminPage.s40') }}</p></details>
          </template>
          <p v-else class="muted">{{ $t('admin.EmojiAdminPage.s41') }}</p>
          <div class="editor-options">
            <template v-if="!editing?.domain"><label class="check"><input v-model="form.local_only" type="checkbox" :disabled="busy" /><span>{{ $t('admin.EmojiAdminPage.s42') }}<small>{{ $t('admin.EmojiAdminPage.s43') }}</small></span></label><label class="check"><input v-model="form.is_sensitive" type="checkbox" :disabled="busy" /><span>{{ $t('admin.EmojiAdminPage.s44') }}<small>{{ $t('admin.EmojiAdminPage.s45') }}</small></span></label></template>
            <label class="check"><input v-model="form.visible_in_picker" type="checkbox" :disabled="busy" /><span>{{ $t('admin.EmojiAdminPage.s46') }}</span></label>
            <label class="check"><input v-model="form.disabled" type="checkbox" :disabled="busy" /><span>{{ $t('admin.EmojiAdminPage.s47') }}<small>{{ $t('admin.EmojiAdminPage.s48') }}</small></span></label>
          </div>
        </div>
      </div>
      <div class="editor-footer"><button type="button" class="admin-button" :disabled="busy" @click="closeEditor">{{ $t('admin.EmojiAdminPage.s49') }}</button><button class="admin-button admin-button--primary" :disabled="busy">{{ busy ? $t('admin.EmojiAdminPage.s50') : $t('admin.EmojiAdminPage.s51') }}</button></div>
    </form>

    <form v-if="copySource" class="admin-card copy-editor" :aria-label="$t('admin.EmojiAdminPage.s52')" @submit.prevent="copy"><h2>{{ $t('admin.EmojiAdminPage.s53') }}</h2><p class="muted">{{ $t('admin.EmojiAdminPage.s54', [copySource.shortcode]) }}</p><label>{{ $t('admin.EmojiAdminPage.s55') }}<input v-model="copyName" required pattern="[a-zA-Z0-9_]+" maxlength="36" :disabled="busy" /></label><div class="editor-footer"><button type="button" class="admin-button" :disabled="busy" @click="copySource = null">{{ $t('admin.EmojiAdminPage.s49') }}</button><button class="admin-button admin-button--primary" :disabled="busy">{{ $t('admin.EmojiAdminPage.s56') }}</button></div></form>

    <div v-if="loading" class="admin-card emoji-empty" role="status">{{ $t('admin.EmojiAdminPage.s57') }}</div>
    <template v-else>
      <div v-if="items.length" class="emoji-results-heading"><label class="check"><input type="checkbox" :checked="allSelected" :disabled="busy" @change="toggleAll" />{{ $t('admin.EmojiAdminPage.s58', [selected.length]) }}</label><span class="muted">{{ $t('admin.EmojiAdminPage.s59', [total]) }}</span></div>
      <form v-if="selected.length" class="admin-card batch" :aria-label="$t('admin.EmojiAdminPage.s60')" @submit.prevent="batch">
        <div class="batch-heading"><strong>{{ $t('admin.EmojiAdminPage.s61', [selected.length]) }}</strong><button type="button" class="admin-button" :disabled="busy" @click="selected = []">{{ $t('admin.EmojiAdminPage.s62') }}</button></div>
        <div class="batch-fields"><label>{{ $t('admin.EmojiAdminPage.s63') }}<select v-model="batchAction" :disabled="busy"><option value="enable">{{ $t('admin.EmojiAdminPage.s64') }}</option><option value="disable">{{ $t('admin.EmojiAdminPage.s65') }}</option><option value="delete">{{ $t('admin.EmojiAdminPage.s66') }}</option><template v-if="origin === 'local'"><option value="set_category">{{ $t('admin.EmojiAdminPage.s67') }}</option><option value="set_license">{{ $t('admin.EmojiAdminPage.s68') }}</option><option value="set_aliases">{{ $t('admin.EmojiAdminPage.s69') }}</option><option value="add_aliases">{{ $t('admin.EmojiAdminPage.s70') }}</option><option value="remove_aliases">{{ $t('admin.EmojiAdminPage.s71') }}</option><option value="set_local_only">{{ $t('admin.EmojiAdminPage.s72') }}</option><option value="set_sensitive">{{ $t('admin.EmojiAdminPage.s73') }}</option></template></select></label><label v-if="['set_category', 'set_license', 'set_aliases', 'add_aliases', 'remove_aliases'].includes(batchAction)">{{ $t('admin.EmojiAdminPage.s74') }}<input v-model="batchText" :placeholder="$t('admin.EmojiAdminPage.s75')" :disabled="busy" /></label><label v-if="['set_local_only', 'set_sensitive'].includes(batchAction)" class="check"><input v-model="batchValue" type="checkbox" :disabled="busy" />{{ $t('admin.EmojiAdminPage.s76') }}</label><button class="admin-button" :class="batchAction === 'delete' ? 'admin-button--danger' : 'admin-button--primary'" :disabled="busy || !selected.length">{{ $t('admin.EmojiAdminPage.s77') }}</button></div>
      </form>
      <ul class="emoji-list">
        <li v-for="emoji in items" :key="emoji.id" class="emoji-card" :class="{ 'emoji-card--selected': selected.includes(emoji.id), 'emoji-card--disabled': emoji.disabled }">
          <div class="emoji-card__top"><input v-model="selected" type="checkbox" :value="emoji.id" :aria-label="$t('admin.EmojiAdminPage.s78', [emoji.shortcode])" :disabled="busy" /><span class="emoji-badge" :class="{ 'emoji-badge--disabled': emoji.disabled }">{{ emoji.disabled ? $t('admin.EmojiAdminPage.s79') : $t('admin.EmojiAdminPage.s80') }}</span></div>
          <button type="button" class="emoji-card__artwork" :aria-label="$t('admin.EmojiAdminPage.s81', [emoji.shortcode])" :disabled="busy" @click="openEditor(emoji)"><img :src="emoji.static_url || emoji.url" :alt="`:${emoji.shortcode}:`" loading="lazy" /></button>
          <div class="emoji-info"><strong>:{{ emoji.shortcode }}:</strong><p class="muted">{{ emoji.category || $t('admin.EmojiAdminPage.s82') }} · {{ emoji.domain || $t('admin.EmojiAdminPage.s83') }}</p><p v-if="emoji.aliases?.length" class="muted">{{ $t('admin.EmojiAdminPage.s84', [emoji.aliases.join('、')]) }}</p><div class="emoji-flags"><span v-if="emoji.local_only">{{ $t('admin.EmojiAdminPage.s85') }}</span><span v-if="emoji.is_sensitive">{{ $t('admin.EmojiAdminPage.s44') }}</span><span v-if="!emoji.visible_in_picker">{{ $t('admin.EmojiAdminPage.s86') }}</span></div></div>
          <div class="actions"><button type="button" class="admin-button" :disabled="busy" @click="openEditor(emoji)">{{ $t('admin.EmojiAdminPage.s87') }}</button><button v-if="emoji.domain" type="button" class="admin-button" :disabled="busy" @click="copySource = emoji; copyName = emoji.shortcode">{{ $t('admin.EmojiAdminPage.s88') }}</button><button type="button" class="admin-button admin-button--danger" :disabled="busy" @click="remove(emoji)">{{ $t('admin.EmojiAdminPage.s66') }}</button></div>
        </li>
      </ul>
      <div v-if="!items.length && !error" class="admin-card emoji-empty"><t-icon name="happy" :size="36" aria-hidden="true" /><h2>{{ $t('admin.EmojiAdminPage.s89') }}</h2><p class="muted">{{ $t('admin.EmojiAdminPage.s90') }}</p></div>
      <nav class="emoji-pagination" :aria-label="$t('admin.EmojiAdminPage.s91')"><button class="admin-button" :disabled="busy || page <= 1" @click="page--; load()">{{ $t('admin.EmojiAdminPage.s92') }}</button><span class="muted">{{ $t('admin.EmojiAdminPage.s93', [page, pages, total]) }}</span><button class="admin-button" :disabled="busy || page >= pages" @click="page++; load()">{{ $t('admin.EmojiAdminPage.s94') }}</button></nav>
    </template>
  </div>
</template>

<style scoped>
.emoji-admin.admin-page { min-width: 0; background: transparent; border: 0; border-radius: 0; overflow: visible; }
.emoji-heading, .emoji-section-heading, .emoji-results-heading, .batch-heading { display: flex; align-items: center; justify-content: space-between; gap: 16px; }
.emoji-heading > div, .emoji-section-heading > div { min-width: 0; }
.emoji-heading > button { flex-shrink: 0; }
.emoji-eyebrow { color: var(--admin-accent); font-size: 11px; font-weight: 600; letter-spacing: 2px; margin: 0 0 8px; }
.emoji-heading h1 { margin-bottom: 8px; }
.muted { color: var(--color-label-secondary); font-size: 12px; line-height: 1.6; overflow-wrap: anywhere; }
.emoji-admin label { display: grid; gap: 7px; min-width: 0; font-size: 13px; font-weight: 500; }
.emoji-admin input:not([type=checkbox]), .emoji-admin select { width: 100%; min-width: 0; max-width: 100%; min-height: 42px; padding: 9px 12px; color: var(--color-label); background: var(--color-bg-row); border: 1px solid var(--color-separator); border-radius: 9px; box-sizing: border-box; }
.emoji-admin input[type=checkbox] { width: 17px; height: 17px; flex-shrink: 0; accent-color: var(--admin-accent); }
.emoji-search { display: flex; flex-wrap: wrap; gap: 14px; align-items: end; }
.emoji-search label { flex: 1 1 130px; }
.emoji-search .emoji-search__query { flex: 2 1 210px; }
.emoji-search > button { min-height: 42px; }
.emoji-feedback { margin: 0; padding: 14px 18px; border-radius: 12px; overflow-wrap: anywhere; }
.emoji-feedback--success { display: flex; gap: 8px; background: color-mix(in srgb, var(--admin-accent) 7%, var(--color-bg-row)); }
.editor { display: grid; gap: 24px; scroll-margin-top: 20px; }
.editor h2, .copy-editor h2 { margin: 0; }
.editor-grid { display: grid; grid-template-columns: minmax(0, 220px) minmax(0, 1fr); gap: 28px; }
.editor-artwork, .editor-fields { min-width: 0; }
.editor-fields { display: grid; gap: 18px; }
.editor-fields__pair { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; }
.preview-stage, .emoji-card__artwork { background-color: var(--color-bg-grouped); background-image: repeating-conic-gradient(transparent 0% 25%, color-mix(in srgb, var(--color-label) 3%, transparent) 0% 50%); background-size: 18px 18px; }
.preview-stage { min-height: 180px; border-radius: 14px; display: grid; place-items: center; padding: 20px; margin-bottom: 14px; }
.preview { width: 110px; height: 110px; object-fit: contain; }
.preview-empty { display: grid; justify-items: center; gap: 12px; font-size: 12px; color: var(--color-label-secondary); }
.upload-label { position: relative; cursor: pointer; }
.upload-label input[type=file] { position: absolute; width: 1px; height: 1px; min-height: 0; padding: 0; opacity: 0; clip-path: inset(50%); }
.upload-button { display: flex; align-items: center; justify-content: center; gap: 8px; min-height: 42px; border: 1px solid var(--color-separator); border-radius: 9px; background: var(--color-bg-row); font-size: 13px; }
.upload-label:focus-within .upload-button { outline: 3px solid var(--admin-accent); outline-offset: 2px; }
.upload-label input::file-selector-button { border: 0; border-radius: 5px; padding: 6px 8px; background: var(--color-fill); color: var(--color-label); margin-right: 8px; cursor: pointer; }
.upload-hint { font-size: 11px; }
.role-options { min-width: 0; border: 1px solid var(--color-separator); border-radius: 10px; padding: 14px; }
.role-options summary { cursor: pointer; font-size: 13px; overflow-wrap: anywhere; }
.role-options summary span { margin-left: 8px; }
.editor-options { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; padding-top: 6px; }
.emoji-admin .check { display: flex; align-items: center; gap: 9px; min-height: 32px; font-weight: 400; }
.check small { display: block; font-size: 11px; color: var(--color-label-secondary); margin-top: 3px; }
.editor-footer { display: flex; justify-content: flex-end; gap: 10px; border-top: 1px solid var(--color-separator); padding-top: 18px; }
.copy-editor { display: grid; gap: 16px; }
.batch { background: color-mix(in srgb, var(--admin-accent) 5%, var(--color-bg-row)); border-color: color-mix(in srgb, var(--admin-accent) 24%, var(--color-separator)); display: grid; gap: 14px; }
.batch-heading strong { font-size: 14px; }
.batch-fields { display: flex; flex-wrap: wrap; gap: 12px; align-items: end; }
.batch-fields > label { flex: 1 1 140px; }
.emoji-list { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 210px), 1fr)); gap: 16px; list-style: none; padding: 0; margin: 0; }
.emoji-card { display: flex; flex-direction: column; min-width: 0; padding: 14px; border: 1px solid var(--color-separator); border-radius: 16px; background: var(--color-bg-row); transition: border-color .15s, box-shadow .15s; }
.emoji-card:hover { border-color: color-mix(in srgb, var(--admin-accent) 45%, var(--color-separator)); }
.emoji-card--selected { border-color: var(--admin-accent); box-shadow: 0 0 0 1px var(--admin-accent); }
.emoji-card__top { display: flex; align-items: center; justify-content: space-between; gap: 8px; margin-bottom: 12px; }
.emoji-badge, .emoji-flags span { font-size: 10px; border-radius: 5px; padding: 3px 7px; background: var(--color-fill); color: var(--color-label-secondary); }
.emoji-badge--disabled { color: var(--color-danger, #b42318); }
.emoji-card__artwork { width: 100%; height: 112px; display: grid; place-items: center; border: 0; border-radius: 10px; margin-bottom: 14px; cursor: pointer; }
.emoji-card__artwork img { width: 64px; height: 64px; object-fit: contain; }
.emoji-card--disabled .emoji-card__artwork img { opacity: .5; }
.emoji-info { flex: 1; min-width: 0; overflow-wrap: anywhere; }
.emoji-info strong { display: block; font-size: 13px; }
.emoji-info p { margin: 5px 0; font-size: 11px; }
.emoji-flags { display: flex; flex-wrap: wrap; gap: 4px; margin: 8px 0; }
.actions { display: flex; flex-wrap: wrap; gap: 7px; margin-top: 12px; padding-top: 12px; border-top: 1px solid var(--color-separator); }
.actions .admin-button { flex: 1; padding: 7px 9px; font-size: 12px; min-height: 34px; }
.emoji-pagination { display: flex; justify-content: center; align-items: center; flex-wrap: wrap; gap: 14px; }
.emoji-empty { text-align: center; padding: 44px 20px; color: var(--color-label-secondary); }
.emoji-empty h2 { margin: 16px 0 8px; font-size: 16px; color: var(--color-label); }
@media (max-width: 760px) { .emoji-heading { align-items: flex-start; flex-direction: column; } .editor-grid { grid-template-columns: minmax(0, 1fr); gap: 20px; } .editor-artwork { max-width: 100%; } .preview-stage { min-height: 150px; } .editor-fields__pair, .editor-options { grid-template-columns: minmax(0, 1fr); } .emoji-search label, .emoji-search .emoji-search__query { flex-basis: 100%; } .emoji-search > button { width: 100%; } .emoji-pagination span { order: 3; width: 100%; text-align: center; } }
@media (prefers-reduced-motion: reduce) { .emoji-card { transition: none; } }
</style>
