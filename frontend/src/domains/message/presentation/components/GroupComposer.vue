<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { isAxiosError } from 'axios'
import { groupApi } from '../../infrastructure/groupApi'
import { fileSize, groupCanSend, type Group, type GroupMember, type GroupMessage, type GroupSend, type GroupSendResult } from '../../domain/group'
import EmojiPicker from '@/shared/components/post/EmojiPicker.vue'
import type { MastodonEmoji } from '@/shared/types/mastodon'
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
    if (files.value.length >= (props.group.limits?.max_attachments ?? 8)) { error.value = `每次最多添加 ${props.group.limits?.max_attachments ?? 8} 个附件。`; break }
    if (file.size > (props.group.limits?.max_upload_bytes ?? 50 * 1024 * 1024)) { error.value = `“${file.name}”超过 ${fileSize(props.group.limits?.max_upload_bytes ?? 50 * 1024 * 1024)}，请选择更小的文件。`; continue }
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
    }).catch(e => { if (alive) { item.state = 'failed'; item.error = item.controller?.signal.aborted ? '上传已取消' : e instanceof Error ? e.message : '上传失败' } }).finally(() => { if (alive) void pumpUploads() })
  }
}
function removeFile(item: PendingFile) { item.controller?.abort(); if (item.preview) URL.revokeObjectURL(item.preview); files.value = files.value.filter(f => f.key !== item.key) }
function retry(item: PendingFile) { item.state = 'waiting'; item.error = ''; void pumpUploads() }
function paste(event: ClipboardEvent) { const incoming = Array.from(event.clipboardData?.files ?? []); if (incoming.length) { event.preventDefault(); addFiles(incoming) } }
function drop(event: DragEvent) { if (event.dataTransfer?.files.length) addFiles(Array.from(event.dataTransfer.files)) }
function insert(text: string) {
  const field = textInput.value, start = field?.selectionStart ?? body.value.length, end = field?.selectionEnd ?? start
  body.value = body.value.slice(0, start) + text + body.value.slice(end)
  requestAnimationFrame(() => { field?.focus(); field?.setSelectionRange(start + text.length, start + text.length) })
  typing()
}
function selectEmoji(emoji: MastodonEmoji) {
  if (emoji.local_only) { error.value = '这个表情仅供本站帖子使用，请选择可对外使用的表情。'; return }
  selectedEmoji.value.push(emoji.shortcode); insert(`:${emoji.shortcode}:`); picker.value = false
}
function mention(member: GroupMember) { mentions.value.push(member.id); insert(`${member.display_name || member.id} `); mentionPicker.value = false }
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
    if (result.failed_upload_ids?.length || result.status === 'partial') { error.value = '部分内容已发送，重试将继续未完成的部分，不会重复发送成功内容。'; return }
    files.value.forEach(file => { if (file.preview) URL.revokeObjectURL(file.preview) }); files.value = []; body.value = ''; selectedEmoji.value = []; mentions.value = []
    frozen.value = null; transaction = crypto.randomUUID(); signature = ''; emit('cancelContext')
  } catch (e) {
    if (!alive) return
    const partial = isAxiosError(e) ? e.response?.data as Partial<GroupSendResult> | undefined : undefined
    if (partial?.messages?.length) emit('sent', partial.messages)
    error.value = partial?.deleted ? '原消息已删除，但新消息尚未全部发送。草稿已保留，请重试完成发送。' : e instanceof Error ? e.message : '发送失败，草稿已保留'
  } finally { if (alive) busy.value = false }
}
function unlockDraft() { frozen.value = null; transaction = crypto.randomUUID(); signature = ''; error.value = '发送结果可能需要等待同步确认。修改后再次发送会作为一条新消息。' }
onBeforeUnmount(() => { alive = false; void stopTyping(); files.value.forEach(f => { f.controller?.abort(); if (f.preview) URL.revokeObjectURL(f.preview) }) })
</script>
<template>
  <form class="group-composer" @submit.prevent="send" @dragover.prevent @drop.prevent="drop">
    <div v-if="reply || editing" class="group-composer__context"><span>{{ editing ? '编辑会删除原消息并重发；原回复与回应保留在原消息上。' : `回复 ${reply?.sender.display_name}：${reply?.body || '附件'}` }}</span><button type="button" aria-label="取消回复或编辑" :disabled="busy || !!frozen" @click="$emit('cancelContext')">×</button></div>
    <p v-if="error" class="group-composer__error" role="alert">{{ error }} <button v-if="frozen && !busy" type="button" @click="unlockDraft">修改草稿</button></p>
    <div v-if="files.length" class="group-composer__files">
      <div v-for="item in files" :key="item.key" class="group-composer__file"><img v-if="item.preview" :src="item.preview" alt="" /><span><strong>{{ item.file.name }}</strong><small>{{ fileSize(item.file.size) }} · {{ item.state === 'ready' ? '待发送' : item.state === 'uploading' ? `上传 ${item.percent}%` : item.error || '等待上传' }}</small><progress v-if="item.state === 'uploading'" :value="item.percent" max="100" :aria-label="`${item.file.name} 上传进度`" /></span><button v-if="item.state === 'failed'" type="button" :disabled="!canSend || busy || !!frozen" @click="retry(item)">重试</button><button type="button" :disabled="busy || !!frozen" :aria-label="`移除 ${item.file.name}`" @click="removeFile(item)">×</button></div>
    </div>
    <textarea ref="textInput" v-model="body" aria-label="群消息" :readonly="busy || !!frozen || !canSend" :placeholder="canSend ? '写一条消息…' : '当前群组无法在网页发送消息'" rows="2" maxlength="20000" @input="typing" @blur="stopTyping" @paste="paste" @keydown.ctrl.enter.prevent="send" @keydown.meta.enter.prevent="send" />
    <div class="group-composer__toolbar"><div><button type="button" :disabled="!canSend || busy || !!frozen" aria-label="添加图片或文件" @click="fileInput?.click()"><t-icon name="file-add" :size="20" /></button><button type="button" :disabled="!canSend || busy || !!frozen" :aria-expanded="picker" aria-label="选择表情" @click="picker = !picker; mentionPicker = false"><t-icon name="happy" :size="20" /></button><button type="button" :disabled="!canSend || busy || !!frozen" :aria-expanded="mentionPicker" aria-label="提及成员" @click="mentionPicker = !mentionPicker; picker = false">@</button></div><small>Ctrl / ⌘ + Enter 发送</small><button class="btn btn--primary btn--sm" type="submit" :disabled="!ready">{{ busy ? '发送中…' : frozen ? '重试发送' : editing ? '删除并重发' : '发送' }}</button></div>
    <input ref="fileInput" type="file" multiple hidden @change="addFiles(Array.from(($event.target as HTMLInputElement).files || [])); ($event.target as HTMLInputElement).value = ''" />
    <div v-if="picker" class="group-composer__picker"><div class="group-composer__unicode"><button v-for="emoji in ['👍','❤️','😂','🎉','🤔','😊','🙏','👀','😢','🚀','✅','✨']" :key="emoji" type="button" :aria-label="emoji" @click="insert(emoji); picker = false">{{ emoji }}</button></div><EmojiPicker @select="selectEmoji" @close="picker = false" /></div>
    <div v-if="mentionPicker" class="group-composer__mentions" aria-label="选择提及成员"><button v-for="member in members.filter(m => m.membership === 'join')" :key="member.id" type="button" @click="mention(member)">{{ member.display_name || member.id }}<small>{{ member.id }}</small></button><p v-if="!members.length">请打开群详情加载成员列表。</p></div>
  </form>
</template>
<style scoped>
.group-composer { position:relative; padding:1rem 1.25rem; background:var(--color-bg); border-top:1px solid var(--color-separator); }.group-composer textarea { width:100%; min-height:74px; max-height:220px; resize:vertical; border:1px solid var(--color-separator); border-radius:12px; background:var(--color-bg-row); color:var(--color-label); padding:.7rem .85rem; font:inherit; line-height:1.5; }.group-composer textarea:focus { outline:2px solid var(--color-accent); outline-offset:2px; }.group-composer__toolbar { display:flex; align-items:center; gap:.5rem; margin-top:.4rem; }.group-composer__toolbar>div { display:flex; gap:.15rem; }.group-composer__toolbar>div button { width:40px; height:40px; border-radius:10px; color:var(--color-label-secondary); font-size:1.15rem; }.group-composer__toolbar>small { flex:1; text-align:right; font-size:.68rem; color:var(--color-label-secondary); }.group-composer button:disabled { opacity:.4; cursor:default; }.group-composer__context { display:flex; gap:.5rem; align-items:center; padding:.6rem .75rem; margin-bottom:.5rem; border-left:3px solid var(--color-accent); background:var(--color-fill); font-size:.8rem; }.group-composer__context>span { flex:1; overflow:hidden; overflow-wrap:anywhere; max-height:3rem; }.group-composer__context button { width:32px; height:32px; }.group-composer__error { margin-bottom:.65rem; font-size:.85rem; color:var(--color-danger); overflow-wrap:anywhere; }.group-composer__error button { color:var(--color-accent-text); text-decoration:underline; }.group-composer__file { display:flex; align-items:center; gap:.65rem; padding:.5rem; border:1px solid var(--color-separator); border-radius:10px; margin-bottom:.4rem; }.group-composer__file img { width:40px; height:40px; object-fit:cover; border-radius:6px; }.group-composer__file>span { min-width:0; flex:1; }.group-composer__file strong,.group-composer__file small { display:block; font-size:.8rem; overflow-wrap:anywhere; }.group-composer__file small { font-size:.7rem; color:var(--color-label-secondary); }.group-composer__file button { min-height:36px; padding:.3rem; }.group-composer progress { width:100%; height:5px; accent-color:var(--color-accent); }.group-composer__picker { position:absolute; bottom:100%; left:1rem; right:1rem; max-height:65dvh; overflow:auto; z-index:5; border:1px solid var(--color-separator); border-radius:16px; padding:.65rem; background:var(--color-bg-row); box-shadow:0 6px 30px #0002; }.group-composer__picker :deep(.emoji-picker) { margin:0; border:0; box-shadow:none; padding:.5rem; }.group-composer__unicode { display:flex; flex-wrap:wrap; }.group-composer__unicode button { width:40px; height:40px; font-size:1.6rem; }.group-composer__mentions { position:absolute; bottom:100%; left:1rem; right:1rem; max-height:240px; overflow:auto; z-index:5; background:var(--color-bg-row); border:1px solid var(--color-separator); border-radius:12px; }.group-composer__mentions button { display:block; text-align:left; width:100%; padding:.75rem; overflow-wrap:anywhere; }.group-composer__mentions small { display:block; color:var(--color-label-secondary); font-size:.7rem; }@media(max-width:480px){.group-composer {padding:.65rem;}.group-composer__toolbar>small{display:none;}.group-composer__toolbar>div{flex:1;}.group-composer__toolbar button {min-height:44px;min-width:44px;}}
</style>
