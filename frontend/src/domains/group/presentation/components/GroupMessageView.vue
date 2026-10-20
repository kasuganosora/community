<script setup lang="ts">
import { computed, ref, watch, onBeforeUnmount } from 'vue'
import { sanitizeHtml, stripHtml } from '@/shared/utils/sanitize'
import { mediaGated, useMediaMode } from '@/shared/composables/useMediaMode'
import { hasNSFWTag, type GroupMessage } from '../../domain/group'
import { groupApi } from '../../infrastructure/groupApi'
import GroupAvatar from './GroupAvatar.vue'
import GroupAttachment from './GroupAttachment.vue'
import GroupSticker from './GroupSticker.vue'
import { languageName, needsTranslation, translationErrorMessage, uiLocale, useTranslationEnabled } from '@/shared/composables/useStatusTranslation'
import { isAxiosError } from 'axios'
import { t } from '@/shared/i18n'
import type { GroupMessageTranslation } from '../../domain/group'
const props = defineProps<{ message: GroupMessage; groupId?: string; groupTopic?: string; reply?: GroupMessage; canSend: boolean; canRedact: boolean; canTranslate?: boolean; busy?: boolean }>()
defineEmits<{ reply: []; edit: []; remove: []; reaction: [key: string, own: boolean]; picker: [] }>()
const { mode: mediaMode } = useMediaMode()
const sensitiveMedia = computed(() => hasNSFWTag(props.groupTopic || '') || hasNSFWTag(props.message.body) || hasNSFWTag(stripHtml(props.message.formatted_body || '')))
const mediaHidden = computed(() => mediaGated({ sensitive: sensitiveMedia.value }, mediaMode.value))
const mediaURLs = ref<Record<string, string>>({})
const mediaControllers = new Map<string, AbortController>()
watch(() => JSON.stringify([...(props.message.custom_emojis ?? []).map(emoji => emoji.static_url || emoji.url), ...(props.message.reactions ?? []).map(reaction => reaction.url).filter(Boolean)]), async () => {
  const sources = new Set([...(props.message.custom_emojis ?? []).map(emoji => emoji.static_url || emoji.url), ...(props.message.reactions ?? []).map(reaction => reaction.url).filter((url): url is string => !!url)])
  for (const [source, controller] of mediaControllers) if (!sources.has(source)) { controller.abort(); mediaControllers.delete(source); if (mediaURLs.value[source]) URL.revokeObjectURL(mediaURLs.value[source]); delete mediaURLs.value[source] }
  for (const source of sources) {
    if (mediaControllers.has(source)) continue
    const controller = new AbortController(); mediaControllers.set(source, controller)
    void groupApi.media(source, controller.signal).then(blob => {
      if (!controller.signal.aborted && /^image\/(png|jpeg|gif|webp)$/.test(blob.type)) mediaURLs.value[source] = URL.createObjectURL(blob)
    }).catch(() => { /* Keep readable shortcode fallback when the private image is unavailable. */ })
  }
}, { immediate: true })
onBeforeUnmount(() => { mediaControllers.forEach(controller => controller.abort()); Object.values(mediaURLs.value).forEach(url => URL.revokeObjectURL(url)) })
// Translation follows the post behaviour: offered only when the instance
// enables it for group messages, the group allows it, and the message is in
// another language than the UI.
const translationEnabled = useTranslationEnabled()
const translation = ref<GroupMessageTranslation | null>(null)
const showTranslation = ref(false), translating = ref(false), translationError = ref('')
const translatable = computed(() => !!props.groupId && props.canTranslate === true && translationEnabled.value && !props.message.redacted && (props.message.kind === 'text' || props.message.kind === 'notice') && !!props.message.body.trim() && needsTranslation(props.message.language))
const translated = computed(() => showTranslation.value ? translation.value : null)
watch(() => [props.message.id, props.message.body, props.message.formatted_body], () => { translation.value = null; showTranslation.value = false; translationError.value = ''; translating.value = false })
async function toggleTranslation() {
  translationError.value = ''
  if (showTranslation.value) { showTranslation.value = false; return }
  if (translation.value) { showTranslation.value = true; return }
  if (translating.value || !props.groupId) return
  translating.value = true
  const id = props.message.id
  try {
    const result = await groupApi.translate(props.groupId, id, uiLocale())
    if (props.message.id !== id) return
    translation.value = result; showTranslation.value = true
  } catch (cause) {
    if (props.message.id !== id) return
    const code = isAxiosError(cause) ? (cause.response?.data as { error_code?: string } | undefined)?.error_code : undefined
    translationError.value = code === 'translation_visibility_restricted' ? t('group.GroupMessageView.translateRestricted') : translationErrorMessage(cause)
  } finally {
    if (props.message.id === id) translating.value = false
  }
}
const stickerAlt = computed(() => props.message.body.trim())
const html = computed(() => {
  if (translated.value) {
    const root = document.createElement('div'); root.innerHTML = sanitizeHtml(translated.value.content)
    return root.innerHTML
  }
  const escaped = props.message.body.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')
  const linked = escaped.replace(/https?:\/\/[^\s<>]+/g, link => `<a href="${link}">${link}</a>`)
  const template = document.createElement('template')
  template.innerHTML = props.message.formatted_body || ''
  template.content.querySelectorAll('img').forEach(image => image.replaceWith(document.createTextNode(image.getAttribute('alt') || '')))
  const root = document.createElement('div'); root.innerHTML = sanitizeHtml(props.message.formatted_body ? template.innerHTML : linked)
  const byCode = new Map((props.message.custom_emojis ?? []).map(emoji => [emoji.shortcode, mediaURLs.value[emoji.static_url || emoji.url]]))
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT), nodes: Text[] = []
  while (walker.nextNode()) nodes.push(walker.currentNode as Text)
  for (const node of nodes) {
    if (node.parentElement?.closest('code,pre')) continue
    let offset = 0; const fragment = document.createDocumentFragment()
    for (const match of node.data.matchAll(/:([a-zA-Z0-9_+-]+):/g)) {
      const src = byCode.get(match[1]!)
      if (!src) continue
      fragment.append(document.createTextNode(node.data.slice(offset, match.index)))
      const image = document.createElement('img'); image.src = src; image.alt = match[0]; image.className = 'custom-emoji'; fragment.append(image)
      offset = match.index + match[0].length
    }
    if (offset) { fragment.append(document.createTextNode(node.data.slice(offset))); node.replaceWith(fragment) }
  }
  return root.innerHTML
})
const createdAt = computed(() => {
  const date = new Date(props.message.created_at)
  if (!Number.isFinite(date.getTime())) return ''
  const today = new Date()
  return date.toDateString() === today.toDateString()
    ? date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
    : date.toLocaleString([], { ...(date.getFullYear() !== today.getFullYear() ? { year: 'numeric' as const } : {}), month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
})
</script>
<template>
  <article class="group-message" :class="{ 'group-message--own': message.own }" :data-message-id="message.id">
    <GroupAvatar :name="message.sender.display_name || message.sender.id" :url="message.sender.avatar_url" :size="32" />
    <div class="group-message__content">
      <header><strong>{{ message.sender.display_name || message.sender.id }}</strong><time :datetime="new Date(message.created_at).toISOString()" :title="new Date(message.created_at).toLocaleString()">{{ createdAt }}</time><span v-if="message.edited">{{ $t('group.GroupMessageView.s1') }}</span></header>
      <p v-if="message.redacted" class="group-message__muted">{{ $t('group.GroupMessageView.s2') }}</p>
      <template v-else>
        <blockquote v-if="message.reply_to">{{ message.reply_unavailable ? $t('group.GroupMessageView.s3') : reply ? (reply.redacted ? $t('group.GroupMessageView.s4') : `${reply.sender.display_name}：${reply.body || $t('group.GroupMessageView.s5')}`) : $t('group.GroupMessageView.s6') }}</blockquote>
        <p v-if="message.kind === 'unsupported'" class="group-message__muted">{{ $t('group.GroupMessageView.s7') }}</p>
        <template v-else-if="message.kind === 'sticker'"><GroupSticker v-for="attachment in message.attachments" :key="`${message.id}:${attachment.id}:${attachment.url}:${mediaMode}`" :attachment="attachment" :alt="stickerAlt" :hidden="mediaHidden" /></template>
        <div v-else-if="html" class="group-message__body" dir="auto" v-html="html" />
        <p v-else-if="message.body" class="group-message__body" dir="auto">{{ message.body }}</p>
        <p v-if="translated" class="group-message__translate-meta">{{ $t('shared.Translation.from', [languageName(translated.detected_source_language || message.language), translated.provider]) }}</p>
        <p v-if="translationError" class="group-message__translate-error" role="alert">{{ translationError }}</p>
        <GroupAttachment v-for="attachment in (message.kind === 'sticker' ? [] : message.attachments)" :key="`${message.id}:${attachment.id}:${attachment.url}:${mediaMode}`" :attachment="attachment" :hidden="mediaHidden" :sensitive="sensitiveMedia" />
        <div class="group-message__reactions"><button v-for="reaction in message.reactions" :key="reaction.key" type="button" :aria-pressed="reaction.own" :disabled="!canSend || busy" :aria-label="$t('group.GroupMessageView.s9', [reaction.label || reaction.key, reaction.count, reaction.own ? $t('group.GroupMessageView.s8') : ''])" @click="$emit('reaction', reaction.key, reaction.own)"><img v-if="reaction.url && mediaURLs[reaction.url]" :src="mediaURLs[reaction.url!]" :alt="reaction.shortcode" referrerpolicy="no-referrer" /><template v-else>{{ reaction.label || reaction.shortcode || reaction.key }}</template> <span>{{ reaction.count }}</span></button></div>
        <div class="group-message__actions">
          <button type="button" :disabled="!canSend || busy" :aria-label="$t('group.GroupMessageView.s10')" @click="$emit('picker')">＋</button>
          <button type="button" :disabled="!canSend || busy" @click="$emit('reply')">{{ $t('group.GroupMessageView.s11') }}</button>
          <button v-if="translatable" type="button" class="group-message__translate" :disabled="translating" :aria-busy="translating || undefined" @click="toggleTranslation"><span v-if="translating" class="group-message__spinner" aria-hidden="true" />{{ translating ? $t('shared.Translation.translating') : translated ? $t('shared.Translation.showOriginal') : $t('shared.Translation.translate') }}</button>
          <button v-if="message.own && message.kind === 'text'" type="button" :disabled="!canSend || busy" @click="$emit('edit')">{{ $t('group.GroupMessageView.s12') }}</button>
          <button v-if="message.own || canRedact" type="button" :disabled="busy" @click="$emit('remove')">{{ $t('group.GroupMessageView.s13') }}</button>
        </div>
      </template>
    </div>
  </article>
</template>
<style scoped>
.group-message { display:flex; align-items:flex-start; gap:.65rem; padding:.8rem 1.25rem; min-width:0; }.group-message__content { min-width:0; flex:1; }.group-message header { display:flex; align-items:baseline; flex-wrap:wrap; gap:.55rem; line-height:1.5; }.group-message header strong { font-size:.9rem; overflow-wrap:anywhere; }.group-message time,.group-message header>span { font-size:.7rem; color:var(--color-label-secondary); }.group-message__body { white-space:pre-wrap; overflow-wrap:anywhere; line-height:1.55; font-size:.95rem; margin-top:.2rem; }.group-message__body :deep(a) { color:var(--color-accent-text); text-decoration:underline; }.group-message__body :deep(pre) { overflow:auto; white-space:pre-wrap; }.group-message blockquote { margin:.4rem 0; padding:.35rem .7rem; border-left:2px solid var(--color-accent); background:var(--color-fill); color:var(--color-label-secondary); font-size:.8rem; max-height:3.7rem; overflow:hidden; overflow-wrap:anywhere; }.group-message__muted { color:var(--color-label-secondary); font-size:.85rem; }.group-message__reactions { display:flex; flex-wrap:wrap; gap:4px; }.group-message__reactions button { margin-top:.4rem; padding:.25rem .55rem; border:1px solid var(--color-separator); border-radius:8px; font-size:.85rem; }.group-message__reactions button[aria-pressed=true] { background:var(--color-accent-soft); border-color:var(--color-accent); }.group-message__body :deep(.custom-emoji) { width:1.5em; height:1.5em; object-fit:contain; vertical-align:middle; }.group-message__reactions img { width:1.4em; height:1.4em; object-fit:contain; vertical-align:middle; }.group-message__reactions span { font-size:.7rem; }.group-message__actions { display:flex; flex-wrap:wrap; gap:.3rem; margin-top:.25rem; }.group-message__actions button { min-height:30px; padding:0 .45rem; color:var(--color-label-secondary); border-radius:6px; font-size:.75rem; }.group-message__actions button:first-child { font-size:1.2rem; }.group-message button:disabled { opacity:.4; cursor:default; }.group-message button:focus-visible { outline:2px solid var(--color-accent); outline-offset:2px; }.group-message__translate { display:inline-flex; align-items:center; gap:.35rem; }.group-message__spinner { width:.8em; height:.8em; border:2px solid currentColor; border-right-color:transparent; border-radius:50%; animation:group-message-spin .8s linear infinite; }@keyframes group-message-spin { to { transform:rotate(360deg); } }@media(prefers-reduced-motion:reduce){.group-message__spinner{animation:none;}}.group-message__translate-meta { margin-top:.25rem; font-size:.72rem; color:var(--color-label-secondary); }.group-message__translate-error { margin-top:.25rem; font-size:.78rem; color:var(--color-danger); }@media(hover:hover) {.group-message:hover { background:var(--color-hover); }.group-message button:hover:not(:disabled) { background:var(--color-fill); }}@media(pointer:coarse){.group-message button{min-height:44px;min-width:44px;}}@media(max-width:480px){.group-message{padding:.75rem;}}
</style>
