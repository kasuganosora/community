<script setup lang="ts">
import { isMobileLayout } from '@/shared/composables/useMobileDock'
import { useUnsavedWork } from '@/shared/composables/useUnsavedWork'
import { useDismiss } from '@/shared/composables/useDismiss'
import { useMediaDrop, acceptAny } from '@/shared/composables/useMediaDrop'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { isAxiosError } from 'axios'
import { groupApi } from '../../infrastructure/groupApi'
import { fileSize, groupCanSend, type Group, type GroupMember, type GroupMessage, type GroupSend, type GroupSendResult } from '../../domain/group'
import EmojiPicker from '@/shared/components/post/EmojiPicker.vue'
import type { MastodonEmoji } from '@/shared/types/mastodon'
import { t } from '@/shared/i18n'
const props = defineProps<{ group: Group; reply?: GroupMessage | null; editing?: GroupMessage | null; members: GroupMember[] }>()
const emit = defineEmits<{ sent: [messages: GroupMessage[]]; cancelContext: []; dirty: [value: boolean]; locked: [value: boolean] }>()
interface PendingFile { key: string; file: File; preview: string; percent: number; state: 'waiting' | 'uploading' | 'ready' | 'failed'; id?: string; error?: string; controller?: AbortController }
const body = ref(''), files = ref<PendingFile[]>([]), busy = ref(false), error = ref(''), picker = ref(false), mentionPicker = ref(false)
const selectedEmoji = ref<string[]>([]), mentions = ref<string[]>([])
const fileInput = ref<HTMLInputElement>(), textInput = ref<HTMLTextAreaElement>()
const canSend = computed(() => groupCanSend(props.group))
const ready = computed(() => canSend.value && !busy.value && (!!body.value.trim() || files.value.length > 0) && files.value.every(f => f.state === 'ready'))
let lastTyping = 0, typingTimer: ReturnType<typeof setTimeout> | undefined, alive = true
const frozen = ref<{ payload: GroupSend; editingId?: string } | null>(null)
let transaction = crypto.randomUUID(), signature = ''
watch([busy, frozen], () => emit('locked', busy.value || !!frozen.value), { immediate: true })
watch(() => props.editing?.id, () => { if (props.editing) { body.value = props.editing.body; textInput.value?.focus() } })
watch([body, files], () => emit('dirty', !!body.value || files.value.length > 0), { deep: true })
// Unsent text survives a deploy auto-reload (see useVersionSync); local
// files cannot be persisted, but their presence defers the reload.
useUnsavedWork<{ text: string }>(() => `group:${props.group.id}`, {
  busy: () => busy.value || !!body.value.trim() || files.value.length > 0,
  snapshot: () => body.value.trim() && !props.editing ? { text: body.value } : null,
  restore: (saved) => { if (typeof saved.text === 'string' && !body.value) body.value = saved.text },
})
const pickerEl = ref<HTMLElement>(), pickerButton = ref<HTMLButtonElement>()
const mentionEl = ref<HTMLElement>(), mentionButton = ref<HTMLButtonElement>()
useDismiss(mentionPicker, () => [mentionEl.value, mentionButton.value], { scroll: false })
useDismiss(picker, () => [pickerEl.value, pickerButton.value], { scroll: false })
watch(canSend, value => { if (!value) { picker.value = false; mentionPicker.value = false; files.value.forEach(f => f.controller?.abort()); void stopTyping() } })
function typing() {
  if (!canSend.value) return
  clearTimeout(typingTimer)
  if (Date.now() - lastTyping > 5000) { lastTyping = Date.now(); void groupApi.typing(props.group.id, true).catch(() => {}) }
  typingTimer = setTimeout(() => { void stopTyping() }, 5000)
}
async function stopTyping() { clearTimeout(typingTimer); if (lastTyping) { lastTyping = 0; await groupApi.typing(props.group.id, false).catch(() => {}) } }
function addFiles(selected: File[]) {
  if (!canSend.value || busy.value || frozen.value) return
  for (const file of selected) {
    if (files.value.length >= (props.group.limits?.max_attachments ?? 8)) { error.value = t('group.GroupComposer.s25', [props.group.limits?.max_attachments ?? 8]); break }
    if (file.size > (props.group.limits?.max_upload_bytes ?? 50 * 1024 * 1024)) { error.value = t('group.GroupComposer.s26', [file.name, fileSize(props.group.limits?.max_upload_bytes ?? 50 * 1024 * 1024)]); continue }
    files.value.push({ key: crypto.randomUUID(), file, preview: /^image\/(png|jpeg|gif|webp)$/.test(file.type) ? URL.createObjectURL(file) : '', percent: 0, state: 'waiting' })
  }
  void pumpUploads()
}
async function pumpUploads() {
  if (!canSend.value || !alive) return
  while (files.value.filter(f => f.state === 'uploading').length < 2) {
    const item = files.value.find(f => f.state === 'waiting')
    if (!item) return
    item.state = 'uploading'; item.controller = new AbortController()
    void groupApi.upload(props.group.id, item.file, item.controller.signal, progress => { item.percent = progress }).then(upload => {
      if (!alive || item.controller?.signal.aborted) return
      item.id = upload.id; item.state = 'ready'; item.percent = 100
    }).catch(e => { if (alive) { item.state = 'failed'; item.error = item.controller?.signal.aborted ? t('group.GroupComposer.s27') : e instanceof Error ? e.message : t('group.GroupComposer.s28') } }).finally(() => { if (alive) void pumpUploads() })
  }
}
function removeFile(item: PendingFile) { item.controller?.abort(); if (item.preview) URL.revokeObjectURL(item.preview); files.value = files.value.filter(f => f.key !== item.key) }
function retry(item: PendingFile) { item.state = 'waiting'; item.error = ''; void pumpUploads() }
const formEl = ref<HTMLFormElement>()
// Shared paste/drop attach; group chat accepts any file type.
const { dragging } = useMediaDrop(formEl, { onFiles: addFiles, enabled: () => canSend.value && !busy.value && !frozen.value, accept: acceptAny })
function keepInputFocus(event: PointerEvent) {
  // Same as the DM composer: blurring a phone textarea re-docks the tab bar
  // between pointerdown and click, so the tapped button (retry, remove,
  // toolbar) moves away and the tap is lost.
  if (isMobileLayout() && document.activeElement === textInput.value &&
    event.target instanceof Element && event.target.closest('button, label')) event.preventDefault()
}
function insert(text: string) {
  const field = textInput.value, start = field?.selectionStart ?? body.value.length, end = field?.selectionEnd ?? start
  body.value = body.value.slice(0, start) + text + body.value.slice(end)
  requestAnimationFrame(() => { field?.focus(); field?.setSelectionRange(start + text.length, start + text.length) })
  typing()
}
function selectEmoji(emoji: MastodonEmoji) {
  if (emoji.local_only) { error.value = t('group.GroupComposer.s29'); return }
  selectedEmoji.value.push(emoji.shortcode); insert(`:${emoji.shortcode}:`); picker.value = false
}
function mention(member: GroupMember) { mentions.value.push(member.id); insert(`${member.display_name || member.id} `); mentionPicker.value = false }
function onSendKeydown(event: KeyboardEvent) {
  if (event.key !== 'Enter' || (!event.ctrlKey && !event.metaKey) || event.isComposing) return
  event.preventDefault()
  void send()
}
async function send() {
  if (!ready.value) return
  const payload: GroupSend = { transaction_id: transaction, body: body.value.trim(), ...(props.reply ? { reply_to: props.reply.id } : {}), upload_ids: files.value.map(f => f.id!), mentions: [...new Set(mentions.value)], custom_emojis: [...new Set(selectedEmoji.value)] }
  const nextSignature = JSON.stringify({ ...payload, transaction_id: '' })
  if (!frozen.value && signature !== nextSignature) { transaction = crypto.randomUUID(); payload.transaction_id = transaction; signature = nextSignature }
  const pending = frozen.value ?? { payload, editingId: props.editing?.id }
  frozen.value = pending; busy.value = true; error.value = ''; void stopTyping()
  try {
    const result = pending.editingId ? await groupApi.replace(props.group.id, pending.editingId, pending.payload) : await groupApi.send(props.group.id, pending.payload)
    if (!alive) return
    emit('sent', result.messages ?? [])
    if (result.failed_upload_ids?.length || result.status === 'partial') { error.value = t('group.GroupComposer.s30'); return }
    files.value.forEach(file => { if (file.preview) URL.revokeObjectURL(file.preview) }); files.value = []; body.value = ''; selectedEmoji.value = []; mentions.value = []
    frozen.value = null; transaction = crypto.randomUUID(); signature = ''; emit('cancelContext')
  } catch (e) {
    if (!alive) return
    const partial = isAxiosError(e) ? e.response?.data as Partial<GroupSendResult> | undefined : undefined
    if (partial?.messages?.length) emit('sent', partial.messages)
    error.value = partial?.deleted ? t('group.GroupComposer.s31') : e instanceof Error ? e.message : t('group.GroupComposer.s32')
  } finally { if (alive) busy.value = false }
}
function unlockDraft() { frozen.value = null; transaction = crypto.randomUUID(); signature = ''; error.value = t('group.GroupComposer.s33') }
onBeforeUnmount(() => { alive = false; void stopTyping(); files.value.forEach(f => { f.controller?.abort(); if (f.preview) URL.revokeObjectURL(f.preview) }) })
</script>
<template>
  <form ref="formEl" class="group-composer" @pointerdown="keepInputFocus" :class="{ 'group-composer--dropping': dragging }" @submit.prevent="send">
    <div v-if="reply || editing" class="group-composer__context"><span>{{ editing ? $t('group.GroupComposer.s1') : $t('group.GroupComposer.s3', [reply?.sender.display_name, reply?.body || $t('group.GroupComposer.s2')]) }}</span><button type="button" :aria-label="$t('group.GroupComposer.s4')" :disabled="busy || !!frozen" @click="$emit('cancelContext')">×</button></div>
    <p v-if="error" class="group-composer__error" role="alert">{{ error }} <button v-if="frozen && !busy" type="button" @click="unlockDraft">{{ $t('group.GroupComposer.s5') }}</button></p>
    <div v-if="files.length" class="group-composer__files">
      <div v-for="item in files" :key="item.key" class="group-composer__file"><img v-if="item.preview" :src="item.preview" alt="" /><span><strong>{{ item.file.name }}</strong><small>{{ fileSize(item.file.size) }} · {{ item.state === 'ready' ? $t('group.GroupComposer.s6') : item.state === 'uploading' ? $t('group.GroupComposer.s7', [item.percent]) : item.error || $t('group.GroupComposer.s8') }}</small><progress v-if="item.state === 'uploading'" :value="item.percent" max="100" :aria-label="$t('group.GroupComposer.s9', [item.file.name])" /></span><button v-if="item.state === 'failed'" type="button" :disabled="!canSend || busy || !!frozen" @click="retry(item)">{{ $t('group.GroupComposer.s10') }}</button><button type="button" :disabled="busy || !!frozen" :aria-label="$t('group.GroupComposer.s11', [item.file.name])" @click="removeFile(item)">×</button></div>
    </div>
    <div class="group-composer__surface">
    <textarea ref="textInput" v-model="body" :aria-label="$t('group.GroupComposer.s12')" :readonly="busy || !!frozen || !canSend" :placeholder="canSend ? $t('group.GroupComposer.s13') : $t('group.GroupComposer.s14')" rows="2" maxlength="20000" @input="typing" @blur="stopTyping" @keydown="onSendKeydown" />
    <div class="group-composer__toolbar"><div><button type="button" :disabled="!canSend || busy || !!frozen" :aria-label="$t('group.GroupComposer.s15')" @click="fileInput?.click()"><t-icon name="file-add" :size="20" /></button><button type="button" :disabled="!canSend || busy || !!frozen" ref="pickerButton" :aria-expanded="picker" :aria-label="$t('group.GroupComposer.s16')" @click="picker = !picker; mentionPicker = false"><t-icon name="happy" :size="20" /></button><button type="button" :disabled="!canSend || busy || !!frozen" ref="mentionButton" :aria-expanded="mentionPicker" :aria-label="$t('group.GroupComposer.s17')" @click="mentionPicker = !mentionPicker; picker = false">@</button></div><small>{{ $t('group.GroupComposer.s18') }}</small><button class="btn btn--primary btn--sm" type="submit" :disabled="!ready">{{ busy ? $t('group.GroupComposer.s19') : frozen ? $t('group.GroupComposer.s20') : editing ? $t('group.GroupComposer.s21') : $t('group.GroupComposer.s22') }}</button></div>
    </div>
    <input ref="fileInput" type="file" multiple hidden @change="addFiles(Array.from(($event.target as HTMLInputElement).files || [])); ($event.target as HTMLInputElement).value = ''" />
    <div v-if="picker" ref="pickerEl" class="group-composer__picker"><div class="group-composer__unicode"><button v-for="emoji in ['👍','❤️','😂','🎉','🤔','😊','🙏','👀','😢','🚀','✅','✨']" :key="emoji" type="button" :aria-label="emoji" @click="insert(emoji); picker = false">{{ emoji }}</button></div><EmojiPicker @select="selectEmoji" @close="picker = false" /></div>
    <div v-if="mentionPicker" ref="mentionEl" class="group-composer__mentions" :aria-label="$t('group.GroupComposer.s23')"><button v-for="member in members.filter(m => m.membership === 'join')" :key="member.id" type="button" @click="mention(member)">{{ member.display_name || member.id }}<small>{{ member.id }}</small></button><p v-if="!members.length">{{ $t('group.GroupComposer.s24') }}</p></div>
  </form>
</template>
<style scoped>
.group-composer { container-type:inline-size; position:relative; flex:none; padding:var(--space-3) var(--space-5) var(--space-4); background:var(--color-bg); border-top:1px solid var(--color-separator); }
.group-composer__surface { border:1px solid var(--color-separator); border-radius:var(--radius-lg); background:var(--color-bg-grouped); overflow:hidden; }
.group-composer__surface:has(textarea:focus-visible) { border-color:var(--color-accent); }
.group-composer textarea { display:block; width:100%; min-height:72px; max-height:min(220px,30dvh); field-sizing:content; resize:none; overflow-y:auto; border:0; border-radius:0; background:transparent; color:var(--color-label); padding:var(--space-3) var(--space-4); font:inherit; font-size:1rem; line-height:var(--leading-body); }
.group-composer .group-composer__surface textarea:focus-visible { outline:none; box-shadow:none; }
.group-composer__toolbar { display:flex; flex-wrap:wrap; align-items:center; gap:var(--space-2); padding:var(--space-1) var(--space-2) var(--space-2); }
.group-composer__toolbar>div { display:flex; flex:1; gap:var(--space-1); }
.group-composer__toolbar>div button { width:var(--hit-target); height:var(--hit-target); border-radius:10px; color:var(--color-label-secondary); font-size:1.15rem; }
.group-composer__toolbar>small { display:none; font-size:var(--text-caption-2); color:var(--color-label-secondary); }
.group-composer__toolbar>button { min-height:var(--hit-target); flex:none; padding-inline:var(--space-4); }
@container(min-width:28rem) { .group-composer__toolbar>small { display:block; } }
.group-composer--dropping .group-composer__surface { outline:2px dashed var(--color-accent); outline-offset:2px; }
.group-composer button:disabled { opacity:.4; cursor:default; }.group-composer__context { display:flex; gap:.5rem; align-items:center; padding:.6rem .75rem; margin-bottom:.5rem; border-left:3px solid var(--color-accent); background:var(--color-fill); font-size:.8rem; }.group-composer__context>span { flex:1; overflow:hidden; overflow-wrap:anywhere; max-height:3rem; }.group-composer__context button { width:32px; height:32px; }.group-composer__error { margin-bottom:.65rem; font-size:.85rem; color:var(--color-danger); overflow-wrap:anywhere; }.group-composer__error button { color:var(--color-accent-text); text-decoration:underline; }.group-composer__file { display:flex; align-items:center; gap:.65rem; padding:.5rem; border:1px solid var(--color-separator); border-radius:10px; margin-bottom:.4rem; }.group-composer__file img { width:40px; height:40px; object-fit:cover; border-radius:6px; }.group-composer__file>span { min-width:0; flex:1; }.group-composer__file strong,.group-composer__file small { display:block; font-size:.8rem; overflow-wrap:anywhere; }.group-composer__file small { font-size:.7rem; color:var(--color-label-secondary); }.group-composer__file button { min-height:36px; padding:.3rem; }.group-composer progress { width:100%; height:5px; accent-color:var(--color-accent); }.group-composer__picker { position:absolute; bottom:100%; left:1rem; right:1rem; max-height:65dvh; overflow:auto; z-index:5; border:1px solid var(--color-separator); border-radius:16px; padding:.65rem; background:var(--color-bg-row); box-shadow:var(--shadow-popover); }.group-composer__picker :deep(.emoji-picker) { margin:0; border:0; box-shadow:none; padding:.5rem; }.group-composer__unicode { display:flex; flex-wrap:wrap; }.group-composer__unicode button { width:40px; height:40px; font-size:1.6rem; }.group-composer__mentions { position:absolute; bottom:100%; left:1rem; right:1rem; max-height:240px; overflow:auto; z-index:5; background:var(--color-bg-row); border:1px solid var(--color-separator); border-radius:12px; }.group-composer__mentions button { display:block; text-align:left; width:100%; padding:.75rem; overflow-wrap:anywhere; }.group-composer__mentions small { display:block; color:var(--color-label-secondary); font-size:.7rem; }.group-composer :is(button):focus-visible { outline:2px solid var(--color-accent); outline-offset:2px; }
.group-composer__toolbar>div button:active:not(:disabled) { background:var(--color-pressed); }
@media(hover:hover) and (pointer:fine) { .group-composer__toolbar>div button:hover:not(:disabled) { background:var(--color-hover); } }
@media(max-width:480px){.group-composer {padding:var(--space-2) var(--space-3) var(--space-3);}.group-composer__toolbar>small{display:none;}.group-composer__toolbar>div{flex:1;}.group-composer__toolbar button {min-height:44px;min-width:44px;}}
@media(forced-colors:active) {
  .group-composer__surface:has(textarea:focus-visible) { outline:2px solid Highlight; outline-offset:2px; }
}
</style>
