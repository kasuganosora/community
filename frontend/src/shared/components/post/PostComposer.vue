<script setup lang="ts">
import { useUnsavedWork } from '@/shared/composables/useUnsavedWork'
import { useDismiss } from '@/shared/composables/useDismiss'
import { displayAccount } from '@/shared/utils/accountDisplay'
import { ref, computed, reactive, watch, nextTick, onMounted } from 'vue'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { postApi } from '@/domains/post/infrastructure/postApi'
import { mentionAtCaret } from '@/domains/post/domain/mentionQuery'
import { userApi } from '@/domains/user/infrastructure/userApi'
import { sameOriginMediaUrl } from '@/shared/utils/mediaUrl'
import { stripHtml } from '@/shared/utils/sanitize'
import { markBidiNumbers, stripBidiMarks } from '@/shared/utils/bidi'
import { MAX_POST_LENGTH } from '@/shared/utils/constants'
import type { MastodonAccount, MastodonAttachment, MastodonStatus, Visibility } from '@/shared/types/mastodon'
import {
  MAX_MEDIA_ATTACHMENTS, MIN_POLL_OPTIONS,
  buildCreateParams, composeLength, replyMentions, replyVisibility, validateDraft, visibilityChoices,
  REPLACE_NOTE, REPLACE_ACTION, REPLACE_FAILED, DIRECT_SCOPE,
  type ComposeDraft, type PollDraft,
} from '@/domains/post/domain/compose'
import EmojiPicker from './EmojiPicker.vue'
import PollDeadlinePicker from './PollDeadlinePicker.vue'
import { usePollLimits } from '@/shared/composables/usePollLimits'
import { useMediaDrop, acceptMedia } from '@/shared/composables/useMediaDrop'
import EmojiText from '@/shared/components/common/EmojiText.vue'
import { insertShortcode } from '@/domains/emoji/domain/emoji'
import type { MastodonEmoji } from '@/shared/types/mastodon'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'

import { t } from '@/shared/i18n'
const props = defineProps<{
  /** Compose a reply to this status (prefills mentions and visibility). */
  replyTo?: MastodonStatus | null
  /** Replace this own status by deleting it and publishing a new status. */
  editStatus?: MastodonStatus | null
  placeholder?: string
  autofocus?: boolean
  /** Visibility for new (non-reply) posts, e.g. 'direct' on the messages page. */
  initialVisibility?: Visibility
  /** Embed this public post. */
  quoteTo?: MastodonStatus | null
}>()

const emit = defineEmits<{
  /** The newly created status returned by the API. */
  posted: [status: MastodonStatus]
  cancel: []
}>()

const authStore = useAuthStore()
const uid = Math.random().toString(36).slice(2, 8)

const content = ref('')
const composerRoot = ref<HTMLElement>()
const pickerOpen = ref(false)
const emojiPickerEl = ref<{ $el: HTMLElement }>()
const emojiButton = ref<HTMLButtonElement>()
let selection = { start: 0, end: 0 }
function toggleEmojiPicker() {
  const input = composerRoot.value?.querySelector('textarea')
  selection = { start: input?.selectionStart ?? content.value.length, end: input?.selectionEnd ?? content.value.length }
  pickerOpen.value = !pickerOpen.value
}
async function closeEmojiPicker() {
  pickerOpen.value = false
  await nextTick()
  composerRoot.value?.querySelector<HTMLButtonElement>(t('shared.PostComposer.s25'))?.focus({ preventScroll: true })
}
async function insertEmoji(emoji: MastodonEmoji) {
  const inserted = insertShortcode(content.value, emoji.shortcode, selection.start, selection.end)
  content.value = inserted.text
  pickerOpen.value = false
  await nextTick()
  const input = composerRoot.value?.querySelector('textarea')
  input?.focus()
  input?.setSelectionRange(inserted.cursor, inserted.cursor)
}
async function insertMarker(marker: '#' | '@') {
  if (posting.value || loadingSource.value) return
  const input = composerRoot.value?.querySelector('textarea')
  const start = input?.selectionStart ?? content.value.length
  const end = input?.selectionEnd ?? start
  const prefix = start > 0 && !/\s/.test(content.value[start - 1]!) ? ' ' : ''
  content.value = content.value.slice(0, start) + prefix + marker + content.value.slice(end)
  const cursor = start + prefix.length + 1
  await nextTick()
  input?.focus({ preventScroll: true })
  input?.setSelectionRange(cursor, cursor)
  scheduleMentions()
}
const spoilerEnabled = ref(false)
const spoilerText = ref('')
let composing = false

/** Invisible marks keep numbers in place while typing. They are stripped before posting. */
function holdBidiCaret(selector: string, value: string, next: string) {
  const input = composerRoot.value?.querySelector(selector) as HTMLInputElement | HTMLTextAreaElement | null
  const start = input?.selectionStart ?? next.length
  const end = input?.selectionEnd ?? start
  const map = (pos: number) => {
    let i = 0
    let j = 0
    while (i < pos && j < next.length) {
      if (next[j] === '\u200e' && value[i] !== '\u200e') { j++; continue }
      i++
      j++
    }
    return j
  }
  void nextTick(() => {
    const el = composerRoot.value?.querySelector(selector) as HTMLInputElement | HTMLTextAreaElement | null
    el?.setSelectionRange(map(start), map(end))
  })
}

watch(content, (value) => {
  if (composing) return
  const next = markBidiNumbers(value)
  if (next === value) return
  holdBidiCaret('textarea', value, next)
  content.value = next
})
watch(spoilerText, (value) => {
  if (composing) return
  const next = markBidiNumbers(value)
  if (next === value) return
  holdBidiCaret('input.composer__spoiler', value, next)
  spoilerText.value = next
})

onMounted(() => {
  const root = composerRoot.value
  if (!root) return
  root.addEventListener('compositionstart', () => { composing = true })
  root.addEventListener('compositionend', () => {
    composing = false
    const next = markBidiNumbers(content.value)
    if (next !== content.value) content.value = next
    const spoiler = markBidiNumbers(spoilerText.value)
    if (spoiler !== spoilerText.value) spoilerText.value = spoiler
  })
})
const visibility = ref<Visibility>('public')
const visibilityOpen = ref(false)
const visibilityTrigger = ref<HTMLButtonElement>()
const visibilityMenu = ref<HTMLUListElement>()

async function toggleVisibility() {
  visibilityOpen.value = !visibilityOpen.value
  if (!visibilityOpen.value) return
  await nextTick()
  visibilityMenu.value?.querySelector<HTMLButtonElement>('[aria-selected="true"]')?.focus()
}

function chooseVisibility(value: Visibility) {
  visibility.value = value
  visibilityOpen.value = false
  visibilityTrigger.value?.focus()
}

function onVisibilityKeydown(e: KeyboardEvent) {
  if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(e.key)) return
  e.preventDefault()
  const options = Array.from(visibilityMenu.value?.querySelectorAll<HTMLButtonElement>('[role="option"]') ?? [])
  const index = options.indexOf(document.activeElement as HTMLButtonElement)
  const next = e.key === 'Home' ? 0 : e.key === 'End' ? options.length - 1
    : (index + (e.key === 'ArrowDown' ? 1 : -1) + options.length) % options.length
  options[next]?.focus()
}
const quote = ref<MastodonStatus | null>(props.quoteTo ?? null)
watch(() => props.quoteTo, (value) => { quote.value = value ?? null })

useDismiss(visibilityOpen, () => [visibilityTrigger.value, visibilityMenu.value], {
  onClose: (reason) => {
    visibilityOpen.value = false
    if (reason === 'escape') visibilityTrigger.value?.focus()
  },
})
useDismiss(pickerOpen, () => [emojiPickerEl.value?.$el as HTMLElement | undefined, emojiButton.value], {
  scroll: false,
  onClose: (reason) => { if (reason === 'escape') void closeEmojiPicker(); else pickerOpen.value = false },
})
const poll = ref<PollDraft | null>(null)
const pollLimits = usePollLimits()
const pollOptionLength = (value: string) => Array.from(value).length
interface MediaItem { key: string; name: string; uploading: boolean; preview: string; attachment?: MastodonAttachment; error?: string; file?: File }
const media = reactive<MediaItem[]>([])
const posting = ref(false)
const loadingSource = ref(!!props.editStatus)
const sourceUnavailable = ref(false)
const error = ref<string | null>(null)
const fileInput = ref<HTMLInputElement>()
const textareaRef = ref<{ focus?: () => void } | null>(null)

const isEdit = computed(() => !!props.editStatus)
const mediaIds = computed(() => media.filter(m => m.attachment).map(m => m.attachment!.id))
const uploading = computed(() => media.some(m => m.uploading))
const uploadFailed = computed(() => media.some(m => !!m.error && !m.uploading))

// Unsent work survives a deploy auto-reload (see useVersionSync).
interface PersistedPost { text: string; spoilerEnabled: boolean; spoilerText: string; visibility: Visibility; attachments: MastodonAttachment[] }
let prefilledText: string | null = null
onMounted(() => { prefilledText ??= content.value })
useUnsavedWork<PersistedPost>(() => props.editStatus ? null : `post:${props.replyTo?.id ?? 'new'}:${props.quoteTo?.id ?? ''}`, {
  busy: () => posting.value || uploading.value || media.length > 0 || (content.value.trim() !== '' && content.value !== prefilledText) || spoilerText.value.trim() !== '',
  snapshot: () => {
    const attachments = media.flatMap(m => m.attachment ? [m.attachment] : [])
    const dirtyText = content.value.trim() !== '' && content.value !== prefilledText
    if (!dirtyText && !spoilerText.value.trim() && !attachments.length) return null
    return { text: content.value, spoilerEnabled: spoilerEnabled.value, spoilerText: spoilerText.value, visibility: visibility.value, attachments }
  },
  restore: (saved) => {
    prefilledText ??= content.value
    if (typeof saved.text === 'string') content.value = saved.text
    spoilerEnabled.value = !!saved.spoilerEnabled
    spoilerText.value = typeof saved.spoilerText === 'string' ? saved.spoilerText : ''
    if (saved.visibility) visibility.value = saved.visibility
    for (const attachment of (saved.attachments ?? []).slice(0, MAX_MEDIA_ATTACHMENTS)) {
      media.push({ key: `restored-${attachment.id}`, name: attachment.description || attachment.id, uploading: false, preview: '', attachment })
    }
  },
})

const choices = computed(() => {
  const ceiling = props.replyTo
    ? replyVisibility(props.replyTo.visibility, props.initialVisibility ?? 'public')
    : (props.initialVisibility ?? 'public')
  return visibilityChoices(ceiling)
})
const draft = computed<ComposeDraft>(() => ({
  text: stripBidiMarks(content.value),
  spoilerEnabled: spoilerEnabled.value,
  spoilerText: stripBidiMarks(spoilerText.value),
  visibility: choices.value.some(v => v.value === visibility.value)
    ? visibility.value
    : (choices.value[0]?.value ?? visibility.value),
  poll: poll.value,
  mediaIds: props.editStatus ? props.editStatus.media_attachments.map(m => m.id) : mediaIds.value,
  inReplyToId: props.replyTo?.id,
  quoteId: props.editStatus ? undefined : quote.value?.id,
}))
const charCount = computed(() => composeLength(draft.value))
const isOverLimit = computed(() => charCount.value > MAX_POST_LENGTH)
const validationError = computed(() => validateDraft(draft.value, MAX_POST_LENGTH, pollLimits.value))
const canPost = computed(() => !validationError.value && !posting.value && !uploading.value && !uploadFailed.value && !loadingSource.value && !sourceUnavailable.value)
const placeholderText = computed(() => props.placeholder
  || (props.replyTo ? t('shared.PostComposer.s26', [displayAccount(props.replyTo.account.acct)]) : t('shared.PostComposer.s27')))
const currentVisibility = computed(() => choices.value.find(v => v.value === draft.value.visibility) ?? choices.value[0]!)
const submitLabel = computed(() => {
  if (posting.value) return isEdit.value ? t('shared.PostComposer.s28') : draft.value.visibility === 'direct' ? t('shared.PostComposer.s29') : t('shared.PostComposer.s30')
  if (isEdit.value) return REPLACE_ACTION
  if (props.replyTo) return t('shared.PostComposer.s31')
  return draft.value.visibility === 'direct' ? t('shared.PostComposer.s32') : t('shared.PostComposer.s33')
})
const postingLabel = computed(() => {
  if (isEdit.value) return t('shared.PostComposer.s34')
  if (draft.value.visibility === 'direct') return t('shared.PostComposer.s35')
  return t('shared.PostComposer.s30')
})

function resetForReply() {
  content.value = props.replyTo ? replyMentions(props.replyTo, authStore.currentUser?.acct) : ''
  visibility.value = replyVisibility(props.replyTo?.visibility, props.initialVisibility ?? 'public')
  if (props.replyTo?.spoiler_text) {
    spoilerEnabled.value = true
    spoilerText.value = props.replyTo.spoiler_text
  }
}

async function loadEditSource() {
  const s = props.editStatus
  if (!s) return
  visibility.value = s.visibility
  spoilerEnabled.value = !!s.spoiler_text
  spoilerText.value = s.spoiler_text || ''
  try {
    const src = await postApi.getSource(s.id)
    content.value = src.text
    spoilerEnabled.value = !!src.spoiler_text
    spoilerText.value = src.spoiler_text || ''
  } catch {
    sourceUnavailable.value = true
    error.value = t('shared.PostComposer.s36')
  } finally {
    loadingSource.value = false
  }
}

watch(() => props.replyTo?.id, resetForReply)
onMounted(async () => {
  if (props.editStatus) await loadEditSource()
  else resetForReply()
  if (props.autofocus) focus()
})

function focus() {
  nextTick(() => {
    textareaRef.value?.focus?.()
  })
}

function toggleSpoiler() {
  spoilerEnabled.value = !spoilerEnabled.value
  if (!spoilerEnabled.value) spoilerText.value = ''
}

function togglePoll() {
  poll.value = poll.value ? null : { options: ['', ''], expiresIn: 86400, multiple: false, mode: 'duration', endsAt: '' }
}

function addPollOption() {
  if (poll.value && poll.value.options.length < pollLimits.value.maxOptions) poll.value.options.push('')
}

function removePollOption(i: number) {
  if (poll.value && poll.value.options.length > MIN_POLL_OPTIONS) poll.value.options.splice(i, 1)
}

function mediaPreview(item: MediaItem) {
  if (item.attachment?.type === 'image') return sameOriginMediaUrl(item.attachment.preview_url || item.attachment.url) || item.preview
  return item.preview
}

function pickFiles() {
  fileInput.value?.click()
}

async function onFilesSelected(e: Event) {
  const input = e.target as HTMLInputElement
  const files = Array.from(input.files || [])
  input.value = ''
  await addFiles(files)
}

const mediaEnabled = () => !isEdit.value && !posting.value && !loadingSource.value && !poll.value

/** One path for the picker, paste and drop: bounded by the attachment limit. */
async function addFiles(files: File[]) {
  if (!files.length || !mediaEnabled()) return
  const room = MAX_MEDIA_ATTACHMENTS - media.length
  if (files.length > room) error.value = t('shared.PostComposer.s37', [MAX_MEDIA_ATTACHMENTS])
  await Promise.all(files.slice(0, Math.max(0, room)).map(async (file) => {
    const preview = file.type.startsWith('image/') ? URL.createObjectURL(file) : ''
    const item = reactive<MediaItem>({ key: `${Date.now()}-${Math.random()}`, name: file.name || t('shared.PostComposer.pastedImage'), uploading: true, preview, file })
    media.push(item)
    await uploadItem(item)
  }))
}

async function uploadItem(item: MediaItem) {
  if (!item.file) return
  item.uploading = true
  item.error = undefined
  try {
    item.attachment = await postApi.uploadMedia(item.file)
  } catch (err) {
    // Keep the item (and its preview) so the upload can be retried.
    item.error = err instanceof Error ? err.message : t('shared.PostComposer.s38')
    error.value = t('shared.PostComposer.s39', [item.name, item.error])
  } finally {
    item.uploading = false
  }
}

function retryMedia(item: MediaItem) {
  error.value = null
  void uploadItem(item)
}

const { dragging: mediaDragging } = useMediaDrop(composerRoot, { onFiles: files => { void addFiles(files) }, enabled: mediaEnabled, accept: acceptMedia })

function removeMedia(i: number) {
  const item = media[i]
  if (item?.preview) URL.revokeObjectURL(item.preview)
  media.splice(i, 1)
}

function reset() {
  for (const item of media) if (item.preview) URL.revokeObjectURL(item.preview)
  content.value = ''
  spoilerEnabled.value = false
  spoilerText.value = ''
  poll.value = null
  media.splice(0, media.length)
  error.value = null
  quote.value = null
  resetForReply()
}

async function handlePost() {
  if (posting.value || uploading.value || loadingSource.value || sourceUnavailable.value) return
  if (validationError.value) {
    error.value = validationError.value
    return
  }
  posting.value = true
  error.value = null
  try {
    let status: MastodonStatus
    if (props.editStatus) {
      status = await postApi.replaceStatus(props.editStatus.id, {
        status: stripBidiMarks(content.value),
        spoiler_text: spoilerEnabled.value ? stripBidiMarks(spoilerText.value).trim() : '',
        sensitive: spoilerEnabled.value && !!spoilerText.value.trim(),
      })
    } else {
      status = await postApi.create(buildCreateParams(draft.value))
    }
    emit('posted', status)
    if (!props.editStatus) reset()
  } catch (e) {
    console.error('Failed to post status:', e)
    const reason = e instanceof Error && e.message ? `：${e.message}` : t('shared.PostComposer.s40')
    error.value = `${isEdit.value ? REPLACE_FAILED : draft.value.visibility === 'direct' ? t('shared.PostComposer.s41') : t('shared.PostComposer.s42')}${reason}`
  } finally {
    posting.value = false
  }
}

const mentions = ref<MastodonAccount[]>([])
const mentionIndex = ref(0)
let mentionRange: { start: number; end: number } | null = null
let mentionTimer = 0
let mentionSeq = 0

function scheduleMentions() {
  window.clearTimeout(mentionTimer)
  const input = composerRoot.value?.querySelector('textarea')
  const query = mentionAtCaret(content.value, input?.selectionStart ?? content.value.length)
  if (!query) {
    mentions.value = []
    mentionRange = null
    return
  }
  mentionRange = { start: query.start, end: query.end }
  const q = query.q
  mentionTimer = window.setTimeout(() => { void loadMentions(q) }, 120)
}

watch(content, () => scheduleMentions())

async function loadMentions(q: string) {
  const seq = ++mentionSeq
  try {
    const rows = await userApi.searchAccounts(q, 6)
    if (seq !== mentionSeq) return
    const input = composerRoot.value?.querySelector('textarea')
    const current = mentionAtCaret(content.value, input?.selectionStart ?? content.value.length)
    if (!current || current.q !== q) return
    mentions.value = rows
    mentionIndex.value = 0
    mentionRange = { start: current.start, end: current.end }
  } catch {
    if (seq === mentionSeq) mentions.value = []
  }
}

function applyMention(account: MastodonAccount) {
  if (!mentionRange) return
  const input = composerRoot.value?.querySelector('textarea')
  if (composing && input) {
    // Rewriting the value under a live IME composition leaves the browser,
    // the IME and the textarea all waiting for a compositionend that never
    // comes, and every later keystroke is swallowed. Blurring commits the
    // composition first; the caret then decides which mention to replace.
    input.blur()
    composing = false
    const current = mentionAtCaret(content.value, input.selectionStart ?? content.value.length)
    if (current && current.start === mentionRange.start) mentionRange = { start: current.start, end: current.end }
  }
  window.clearTimeout(mentionTimer)
  mentionSeq++
  const label = account.acct.includes('@') ? account.acct : account.username
  const insert = `@${label} `
  const cursor = mentionRange.start + insert.length
  content.value = content.value.slice(0, mentionRange.start) + insert + content.value.slice(mentionRange.end)
  mentions.value = []
  mentionRange = null
  void nextTick(() => {
    const el = composerRoot.value?.querySelector('textarea')
    el?.focus()
    el?.setSelectionRange(cursor, cursor)
  })
}

function onMentionKeydown(e: KeyboardEvent) {
  if (visibilityOpen.value && e.key === 'Escape') {
    e.preventDefault()
    visibilityOpen.value = false
    visibilityTrigger.value?.focus()
    return
  }
  if (!mentions.value.length) return
  // Keys pressed mid-composition belong to the IME (Enter commits the pinyin).
  if (e.isComposing || e.keyCode === 229) return
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    e.preventDefault()
    const step = e.key === 'ArrowDown' ? 1 : -1
    mentionIndex.value = (mentionIndex.value + step + mentions.value.length) % mentions.value.length
  } else if (e.key === 'Enter' || e.key === 'Tab') {
    e.preventDefault()
    const account = mentions.value[mentionIndex.value]
    if (account) applyMention(account)
  } else if (e.key === 'Escape') {
    e.preventDefault()
    mentions.value = []
  }
}

function onKeydown(e: KeyboardEvent) {
  // Ctrl/Cmd+Enter posts, like most Mastodon clients.
  if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
    e.preventDefault()
    handlePost()
  }
}

defineExpose({ focus, reset })
</script>

<template>
  <div ref="composerRoot" class="composer" :class="{ 'composer--menu': visibilityOpen, 'composer--dropping': mediaDragging }" @keydown.capture="onMentionKeydown" @keydown="onKeydown">
    <p v-if="isEdit" class="composer__replace-notice" role="note">
      {{ REPLACE_NOTE }}
      <span v-if="editStatus?.poll">{{ $t('shared.PostComposer.s1') }}</span>
    </p>
    <p v-else-if="draft.visibility === 'direct'" class="composer__replace-notice" role="note">
      {{ DIRECT_SCOPE }}
    </p>
    <div class="composer__header">
      <UserAvatar
        v-if="authStore.currentUser"
        :account="authStore.currentUser"
        :size="36"
        class="composer__avatar"
      />
      <div class="composer__header-actions">
        <div v-if="!isEdit && choices.length > 1" class="composer__visibility">
          <button
            ref="visibilityTrigger"
            type="button"
            class="composer__visibility-btn"
            :aria-expanded="visibilityOpen"
            aria-haspopup="listbox"
            :aria-label="$t('shared.PostComposer.s2')"
            :title="currentVisibility.label"
            :disabled="posting || loadingSource"
            :aria-controls="`composer-visibility-${uid}`"
            @click="toggleVisibility"
          >
            <t-icon :name="currentVisibility.icon" :size="16" />
            <span>{{ currentVisibility.short }}</span>
          </button>
          <ul v-if="visibilityOpen" :id="`composer-visibility-${uid}`" ref="visibilityMenu" class="composer__visibility-menu" role="listbox" :aria-label="$t('shared.PostComposer.s2')" @keydown="onVisibilityKeydown">
            <li v-for="v in choices" :key="v.value">
              <button
                type="button"
                class="composer__visibility-option"
                :class="{ 'is-selected': v.value === visibility }"
                role="option"
                :aria-selected="v.value === visibility"
                @click="chooseVisibility(v.value)"
              >
                <t-icon :name="v.icon" :size="16" />
                <span>{{ v.label }}</span>
              </button>
            </li>
          </ul>
        </div>
        <span v-else-if="!isEdit" class="composer__visibility composer__visibility--locked" :title="currentVisibility.label">
          <t-icon :name="currentVisibility.icon" :size="16" />
          <span>{{ currentVisibility.short }}</span>
        </span>
        <t-button
          v-if="isEdit"
          variant="text"
          shape="round"
          size="small"
          @click="emit('cancel')"
        >
          {{ $t('shared.PostComposer.s3') }}
        </t-button>
        <t-button
          class="composer__submit"
          theme="primary"
          size="small"
          :disabled="!canPost"
          :loading="posting"
          @click="handlePost"
        >
          {{ submitLabel }}
          <t-icon v-if="!posting" name="send" :size="17" aria-hidden="true" />
        </t-button>
      </div>
    </div>
    <div class="composer__body">
      <div class="composer__input-area">
        <input
          v-if="spoilerEnabled"
          v-model="spoilerText"
          class="composer__spoiler"
          type="text"
          dir="auto"
          :placeholder="$t('shared.PostComposer.s4')"
          :aria-label="$t('shared.PostComposer.s5')"
          maxlength="500"
        />
        <div class="composer__field">
          <t-textarea
            ref="textareaRef"
            v-model="content"
            :placeholder="placeholderText"
            :aria-label="placeholderText"
            :autosize="{ minRows: poll || media.length ? 1 : 3, maxRows: 30 }"
            class="composer__textarea"
            :class="{ 'composer__textarea--compact': !!poll || media.length > 0 }"
            :disabled="loadingSource || posting"
            @keyup="scheduleMentions"
            @click="scheduleMentions"
          />
          <ul v-if="mentions.length" class="composer__mentions" role="listbox" :aria-label="$t('shared.PostComposer.s6')">
            <li v-for="(account, i) in mentions" :key="account.id">
              <button
                type="button"
                class="composer__mention"
                :class="{ 'composer__mention--active': i === mentionIndex }"
                role="option"
                :aria-selected="i === mentionIndex"
                @mousedown.prevent="applyMention(account)"
              >
                <UserAvatar :account="account" :size="28" />
                <span class="composer__mention-text">
                  <EmojiText class="composer__mention-name" :text="account.display_name || account.username" :emojis="account.emojis" />
                  <span class="composer__mention-acct">@{{ displayAccount(account.acct) }}</span>
                </span>
              </button>
            </li>
          </ul>
        </div>

        <ul v-if="media.length" class="composer__media" :aria-label="$t('shared.PostComposer.s7')">
          <li v-for="(m, i) in media" :key="m.key" class="composer__media-item">
            <img v-if="mediaPreview(m)" :src="mediaPreview(m)" alt="" />
            <span v-else class="composer__media-name">{{ m.uploading ? $t('shared.PostComposer.s8') : m.name }}</span>
            <t-loading v-if="m.uploading" size="small" class="composer__media-spinner" />
            <button
              v-else-if="m.error"
              type="button"
              class="composer__media-retry"
              data-testid="composer-media-retry"
              :aria-label="$t('shared.PostComposer.retryUpload', [m.name])"
              :title="m.error"
              @click="retryMedia(m)"
            >
              <t-icon name="refresh" :size="16" /><span>{{ $t('shared.PostComposer.retry') }}</span>
            </button>
            <button
              v-if="!m.uploading"
              type="button"
              class="composer__media-remove"
              :aria-label="$t('shared.PostComposer.s9', [m.name])"
              @click="removeMedia(i)"
            >
              <t-icon name="close" :size="14" />
            </button>
          </li>
        </ul>

        <fieldset v-if="poll" class="composer__poll">
          <legend class="composer__poll-legend">{{ $t('shared.PostComposer.s10') }}</legend>
          <div v-for="(_, i) in poll.options" :key="i" class="composer__poll-option">
            <div class="composer__poll-input">
              <input
                v-model="poll.options[i]"
                type="text"
                dir="auto"
                :placeholder="$t('shared.PostComposer.s11', [i + 1])"
                :aria-label="$t('shared.PostComposer.s12', [i + 1])"
                :maxlength="pollLimits.maxCharactersPerOption"
              />
              <span
                class="composer__poll-count"
                :class="{ 'composer__poll-count--near': pollOptionLength(poll.options[i] ?? '') >= pollLimits.maxCharactersPerOption - 5 }"
                :aria-label="$t('shared.PostComposer.pollCharLimit', [pollOptionLength(poll.options[i] ?? ''), pollLimits.maxCharactersPerOption])"
              >{{ pollLimits.maxCharactersPerOption - pollOptionLength(poll.options[i] ?? '') }}</span>
            </div>
            <button
              v-if="poll.options.length > MIN_POLL_OPTIONS"
              type="button"
              class="tool-btn composer__poll-remove"
              :aria-label="$t('shared.PostComposer.s13', [i + 1])"
              @click="removePollOption(i)"
            >
              <t-icon name="close" :size="14" />
            </button>
          </div>
          <div class="composer__poll-settings">
            <button
              v-if="poll.options.length < pollLimits.maxOptions"
              type="button"
              class="composer__link-btn"
              @click="addPollOption"
            >
              <t-icon name="add" :size="16" />
              {{ $t('shared.PostComposer.s14') }}
            </button>
            <span v-else class="composer__poll-max">{{ $t('shared.PostComposer.pollMaxOptions', [pollLimits.maxOptions]) }}</span>
            <label class="composer__poll-switch">
              <input v-model="poll.multiple" type="checkbox" role="switch" :aria-checked="poll.multiple" />
              <span class="composer__poll-switch-track" aria-hidden="true"><span class="composer__poll-switch-thumb" /></span>
              {{ $t('shared.PostComposer.s15') }}
            </label>
            <PollDeadlinePicker
              v-model:mode="poll.mode"
              v-model:expires-in="poll.expiresIn"
              v-model:ends-at="poll.endsAt"
              class="composer__poll-deadline"
              :limits="pollLimits"
            />
          </div>
        </fieldset>

        <EmojiPicker v-if="pickerOpen" ref="emojiPickerEl" @select="insertEmoji" @close="closeEmojiPicker" />
        <div v-if="quote && !isEdit" class="composer__quote">
          <span class="composer__quote-who">{{ $t('shared.PostComposer.s17', [displayAccount(quote.account.acct)]) }}</span>
          <p>{{ quote.spoiler_text ? $t('shared.PostComposer.s18', [quote.spoiler_text]) : (stripHtml(quote.content || '').replace(/\s+/g, ' ').trim() || $t('shared.PostComposer.s19')) }}</p>
          <button type="button" class="composer__link-btn" @click="quote = null">{{ $t('shared.PostComposer.s20') }}</button>
        </div>
        <p v-if="posting" class="composer__status" role="status">{{ postingLabel }}</p>
        <div v-else-if="error" class="composer__error" role="alert">
          <span>{{ error }}</span>
          <button v-if="!sourceUnavailable" type="button" class="composer__retry" :disabled="!canPost" @click="handlePost">{{ $t('shared.PostComposer.s21') }}</button>
        </div>
        <div class="composer__toolbar">
          <div class="composer__tools">
            <template v-if="!isEdit">
              <input
                :id="`composer-file-${uid}`"
                ref="fileInput"
                type="file"
                class="sr-only"
                accept="image/*,video/*,audio/*"
                multiple
                tabindex="-1"
                @change="onFilesSelected"
              />
              <button
                type="button"
                class="tool-btn"
                :aria-label="$t('shared.PostComposer.s22')"
                :title="$t('shared.PostComposer.s22')"
                :disabled="posting || loadingSource || !!poll || media.length >= MAX_MEDIA_ATTACHMENTS"
                @click="pickFiles"
              >
                <t-icon name="image-add" :size="20" />
              </button>
              <button
                type="button"
                class="tool-btn"
                :class="{ 'tool-btn--active': !!poll }"
                :aria-pressed="!!poll"
                :aria-label="$t('shared.PostComposer.s10')"
                :title="$t('shared.PostComposer.s10')"
                :disabled="posting || loadingSource || media.length > 0"
                @click="togglePoll"
              >
                <t-icon name="chart-bar" :size="18" />
              </button>
            </template>
            <button
              type="button"
              class="tool-btn"
              :class="{ 'tool-btn--active': spoilerEnabled }"
              :aria-pressed="spoilerEnabled"
              :aria-label="$t('shared.PostComposer.s23')"
              :title="$t('shared.PostComposer.s23')"
              :disabled="posting || loadingSource"
              @click="toggleSpoiler"
            >
              <t-icon name="browse-off" :size="20" />
            </button>
            <button type="button" class="tool-btn" :aria-label="$t('shared.PostComposer.hashtag')" :title="$t('shared.PostComposer.hashtag')" :disabled="posting || loadingSource" @click="insertMarker('#')"><t-icon name="hashtag" :size="20" /></button>
            <button type="button" class="tool-btn tool-btn--mention" :aria-label="$t('shared.PostComposer.mention')" :title="$t('shared.PostComposer.mention')" :disabled="posting || loadingSource" @click="insertMarker('@')"><span aria-hidden="true">@</span></button>
          </div>
          <div class="composer__right">
            <span
              v-if="charCount > 0"
              class="composer__count"
              :class="{ 'composer__count--over': isOverLimit }"
              aria-live="polite"
            >
              {{ charCount }}/{{ MAX_POST_LENGTH }}
            </span>
            <button ref="emojiButton" type="button" class="tool-btn" :class="{ 'tool-btn--active': pickerOpen }" :aria-label="$t('shared.PostComposer.s24')" :title="$t('shared.PostComposer.s24')" :aria-expanded="pickerOpen" :disabled="posting || loadingSource" @click="toggleEmojiPicker"><t-icon name="happy" /></button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.composer {
  margin: var(--space-4) var(--gutter);
  padding: var(--space-3) var(--space-4);
  border: var(--hairline) solid var(--color-separator);
  border-radius: 14px;
  background: var(--color-bg-grouped);
}

.composer--menu {
  position: relative;
  z-index: 4;
}

.composer__replace-notice {
  margin: 0 0 var(--space-3);
  padding: var(--space-3);
  border-radius: var(--radius-sm);
  background: var(--color-fill);
  color: var(--color-label-secondary);
  font-size: var(--text-footnote);
}

.composer__header {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  margin-bottom: var(--space-3);
}

.composer__header-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: var(--space-2);
  min-width: 0;
}

.composer__header-actions :deep(.t-button) {
  min-height: var(--hit-target);
  font-weight: var(--weight-semibold);
}

.composer__submit {
  min-width: 6rem;
  padding-inline: var(--space-4);
  border-radius: 10px !important;
  box-shadow: none;
}

.composer__body {
  min-width: 0;
}

.composer__avatar {
  flex-shrink: 0;
}

.composer__input-area {
  flex: 1;
  min-width: 0;
}

.composer__field {
  position: relative;
}

.composer__mentions {
  position: absolute;
  z-index: 8;
  left: 0;
  right: 0;
  top: calc(100% - var(--space-1));
  margin: 0;
  padding: var(--space-1);
  list-style: none;
  background: var(--color-bg-grouped);
  border: 1px solid var(--color-separator);
  border-radius: var(--radius-md);
  box-shadow: 0 8px 28px rgba(0, 0, 0, 0.45);
}

.composer__mention {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  min-height: 2.75rem;
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-sm);
  text-align: left;
  color: var(--color-label);
}

.composer__mention--active,
.composer__mention:hover {
  background: var(--color-fill);
}

.composer__mention :deep(.user-avatar) {
  pointer-events: none;
}

.composer__mention-text {
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.composer__mention-name {
  font-weight: var(--weight-semibold);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.composer__mention-acct {
  color: var(--color-label-secondary);
  font-size: var(--text-footnote);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* A quiet writing surface with a visible keyboard focus boundary. */
.composer__textarea :deep(.t-textarea__inner),
.composer__textarea :deep(.t-textarea__inner:hover),
.composer__textarea :deep(.t-textarea__inner:focus),
.composer__textarea :deep(.t-textarea__inner.t-is-focused) {
  background: transparent !important;
  border: none !important;
  box-shadow: none !important;
  outline: none !important;
  color: var(--color-label) !important;
  font-size: var(--text-body) !important;
  line-height: 1.65 !important;
  letter-spacing: 0;
  unicode-bidi: plaintext;
  min-height: 6rem !important;
  padding: var(--space-2) 0 !important;
  resize: none !important;
}

.composer__textarea--compact :deep(.t-textarea__inner) {
  min-height: 2.75rem !important;
}

.composer:has(.composer__textarea textarea:focus-visible) {
  border-color: var(--color-accent);
  box-shadow: 0 0 0 2px var(--color-accent-soft);
}

.composer__textarea :deep(.t-textarea__inner::placeholder) {
  color: var(--color-label-tertiary) !important;
}

.composer__toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  padding-top: var(--space-1);
  margin-top: var(--space-1);
}

.composer__tools {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
  margin-left: -6px;
}

.tool-btn {
  display: grid;
  place-items: center;
  width: var(--hit-target);
  height: var(--hit-target);
  border-radius: 8px;
  color: var(--color-label-secondary);
  -webkit-tap-highlight-color: transparent;
  transition:
    background-color var(--duration-fast) var(--ease-out),
    transform var(--duration-release) var(--ease-out);
}

.tool-btn :deep(svg) {
  width: 1.25rem;
  height: 1.25rem;
}

/* Icons that aren't available render nothing; don't leave a hole. */
.tool-btn:empty {
  display: none;
}

@media (hover: hover) and (pointer: fine) {
  .tool-btn:hover {
    background: var(--color-fill);
    color: var(--color-label);
  }
}

.tool-btn:active:not(:disabled) {
  background: var(--color-accent-soft);
  transform: scale(var(--press-scale));
  transition-duration: var(--duration-press);
}

.composer__right {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

.composer__count {
  font-size: var(--text-footnote);
  color: var(--color-label-secondary);
  font-variant-numeric: tabular-nums;
}

.composer__count--over {
  color: var(--color-danger);
  font-weight: var(--weight-semibold);
}

.tool-btn:disabled {
  opacity: 0.35;
  cursor: not-allowed;
}

.tool-btn--mention { font-size: 22px; font-weight: var(--weight-medium); }

.tool-btn--active {
  color: var(--color-accent-text);
  background: var(--color-accent-soft);
}

@media (max-width: 43.74rem) {
  .composer {
    margin: var(--space-3) var(--space-2);
    padding: var(--space-3);
  }

  .composer__right {
    gap: var(--space-1);
  }

  .composer__count {
    font-size: var(--text-caption);
  }
}

@media (max-width: 360px) {
  .composer__toolbar { position: relative; margin-top: 18px; gap: 0; }
  .composer__tools { gap: 0; }
  .composer__count { position: absolute; top: -14px; right: 6px; }
}

/* Secondary fields: filled, borderless, same radius family as inputs. */
.composer__spoiler,
.composer__poll-option input {
  width: 100%;
  min-height: 2.5rem;
  padding: var(--space-2) var(--space-3);
  border: none;
  border-radius: var(--radius-sm);
  background: var(--color-fill);
  color: var(--color-label);
  font: inherit;
  font-size: var(--text-body);
  transition: box-shadow var(--duration-fast) var(--ease-out);
}

.composer__spoiler::placeholder,
.composer__poll-option input::placeholder {
  color: var(--color-label-tertiary);
}

.composer__spoiler:focus-visible,
.composer__poll-option input:focus-visible {
  outline: none;
  box-shadow: 0 0 0 3px var(--color-focus-ring);
}

.composer__spoiler {
  margin: var(--space-1) 0 var(--space-1);
  color: var(--color-warning);
  font-weight: var(--weight-medium);
}

.composer__media {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(6rem, 1fr));
  gap: var(--space-2);
  margin: var(--space-2) 0 0;
  padding: 0;
  list-style: none;
}

.composer__media-item {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  aspect-ratio: 1;
  border-radius: var(--radius-md);
  overflow: hidden;
  background: var(--color-fill);
  box-shadow: inset 0 0 0 var(--hairline) var(--color-separator);
}

.composer__media-item img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.composer__media-name {
  padding: var(--space-1);
  font-size: var(--text-caption);
  color: var(--color-label-secondary);
  word-break: break-all;
}

/* Small glyph on a dark translucent disc, 44pt hit area. */
.composer__media-retry {
  position: absolute;
  inset: auto var(--space-1) var(--space-1) var(--space-1);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
  min-height: 1.75rem;
  border-radius: var(--radius-full);
  background: rgba(0, 0, 0, 0.65);
  color: #fff;
  font-size: 0.75rem;
  font-weight: var(--weight-semibold);
}
.composer__media-retry:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 2px; }
.composer--dropping { outline: 2px dashed var(--color-accent); outline-offset: -2px; background: color-mix(in srgb, var(--color-accent) 6%, transparent); }
.composer__media-remove {
  position: absolute;
  top: var(--space-1);
  right: var(--space-1);
  display: grid;
  place-items: center;
  width: 1.5rem;
  height: 1.5rem;
  border-radius: var(--radius-full);
  background: rgba(0, 0, 0, 0.55);
  -webkit-backdrop-filter: blur(8px);
  backdrop-filter: blur(8px);
  color: #fff;
  transition: transform var(--duration-release) var(--ease-out);
}

.composer__media-remove::after {
  content: '';
  position: absolute;
  inset: -0.625rem;
}

.composer__media-remove:active {
  transform: scale(var(--press-scale-small));
  transition-duration: var(--duration-press);
}

.composer__media-spinner {
  position: absolute;
}

.composer__poll {
  margin: var(--space-2) 0 0;
  padding: var(--space-3);
  border: none;
  border-radius: var(--radius-md);
  background: var(--color-bg-grouped);
}

.composer__poll-legend {
  float: left;
  width: 100%;
  margin-bottom: var(--space-2);
  padding: 0;
  font-size: var(--text-footnote);
  font-weight: var(--weight-semibold);
  letter-spacing: var(--tracking-footnote);
  color: var(--color-label-secondary);
}

.composer__poll-option {
  clear: both;
  display: flex;
  align-items: center;
  gap: var(--space-1);
  margin-bottom: var(--space-2);
}

.composer__poll-option input {
  background: var(--color-bg-row);
  padding-right: 2.75rem;
}

.composer__poll-input {
  position: relative;
  flex: 1;
  min-width: 0;
}

.composer__poll-count {
  position: absolute;
  top: 50%;
  right: var(--space-3);
  transform: translateY(-50%);
  font-size: var(--text-caption);
  font-variant-numeric: tabular-nums;
  color: var(--color-label-tertiary);
  pointer-events: none;
}

.composer__poll-count--near {
  color: var(--color-warning);
}

.composer__poll-settings {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2) var(--space-4);
  font-size: var(--text-subhead);
  color: var(--color-label-secondary);
}

.composer__link-btn {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  min-height: 2rem;
  color: var(--color-accent-text);
  font-weight: var(--weight-medium);
}

.composer__link-btn:active {
  opacity: 0.6;
}

.composer__poll-max {
  font-size: var(--text-footnote);
  color: var(--color-label-tertiary);
}

/* Themed switch; the native checkbox stays for keyboard and screen readers. */
.composer__poll-switch {
  position: relative;
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  min-height: 2.25rem;
  cursor: pointer;
  color: var(--color-label);
}

.composer__poll-switch input {
  position: absolute;
  opacity: 0;
  width: 1px;
  height: 1px;
}

.composer__poll-switch-track {
  position: relative;
  flex: none;
  width: 2.25rem;
  height: 1.375rem;
  border-radius: var(--radius-full);
  background: var(--color-fill-strong);
  transition: background var(--duration-fast) var(--ease-out);
}

.composer__poll-switch-thumb {
  position: absolute;
  top: 2px;
  left: 2px;
  width: 1.125rem;
  height: 1.125rem;
  border-radius: 50%;
  background: #fff;
  box-shadow: 0 1px 3px rgb(0 0 0 / 30%);
  transition: transform var(--duration-fast) var(--ease-out);
}

.composer__poll-switch input:checked + .composer__poll-switch-track {
  background: var(--color-accent);
}

.composer__poll-switch input:checked + .composer__poll-switch-track .composer__poll-switch-thumb {
  transform: translateX(0.875rem);
}

.composer__poll-switch input:focus-visible + .composer__poll-switch-track {
  box-shadow: 0 0 0 3px var(--color-focus-ring);
}

.composer__poll-deadline {
  margin-left: auto;
}

.composer__visibility {
  position: static;
  display: inline-flex;
  align-items: center;
}

.composer__visibility--locked {
  min-height: var(--hit-target);
  padding: 0 var(--space-2);
  gap: var(--space-1);
  border-radius: 8px;
  color: var(--color-label-secondary);
  font-size: var(--text-subhead);
  font-weight: var(--weight-medium);
}

.composer__visibility-btn {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  min-height: var(--hit-target);
  padding: 0 var(--space-2);
  border-radius: 8px;
  color: var(--color-label-secondary);
  font-size: var(--text-subhead);
  font-weight: var(--weight-medium);
}

.composer__visibility-btn:hover { background: var(--color-fill); color: var(--color-label); }

.composer__visibility-btn[aria-expanded='true'] {
  background: var(--color-accent-soft);
}

.composer__visibility-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.composer__visibility-btn::after {
  content: '';
  width: 0.625rem;
  height: 0.625rem;
  background: currentColor;
  -webkit-mask: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 10 10'%3E%3Cpath d='M1.5 3.5 5 7l3.5-3.5' fill='none' stroke='%23000' stroke-width='1.6' stroke-linecap='round' stroke-linejoin='round'/%3E%3C/svg%3E") center / contain no-repeat;
  mask: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 10 10'%3E%3Cpath d='M1.5 3.5 5 7l3.5-3.5' fill='none' stroke='%23000' stroke-width='1.6' stroke-linecap='round' stroke-linejoin='round'/%3E%3C/svg%3E") center / contain no-repeat;
}

.composer__visibility-menu {
  position: absolute;
  z-index: 5;
  top: calc(100% + 4px);
  right: 0;
  width: max-content;
  min-width: min(16rem, 100%);
  max-width: 100%;
  margin: 0;
  padding: var(--space-1);
  list-style: none;
  background: var(--color-bg-grouped);
  color: var(--color-label);
  border: 1px solid var(--color-separator);
  border-radius: var(--radius-md);
  box-shadow: 0 8px 28px rgba(0, 0, 0, 0.45);
}

.composer__visibility-option {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  min-height: var(--hit-target);
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-sm);
  text-align: left;
  color: inherit;
  font-size: var(--text-subhead);
}

.composer__visibility-option.is-selected,
.composer__visibility-option:hover {
  background: var(--color-accent-soft);
  color: var(--color-accent-text);
}

.composer__quote {
  margin-top: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-sm);
  background: var(--color-fill);
  color: var(--color-label-secondary);
  font-size: var(--text-footnote);
}
.composer__quote p { margin: var(--space-1) 0; }
.composer__quote-who { color: var(--color-label); font-weight: var(--weight-semibold); }

.composer__status,
.composer__error {
  color: var(--color-label-secondary);
  font-size: var(--text-footnote);
  padding: var(--space-1) 0;
}

.composer__error {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  color: var(--color-danger);
}

.composer__retry {
  min-height: 2rem;
  padding: 0 var(--space-3);
  border-radius: var(--radius-full);
  background: var(--color-fill);
  color: var(--color-label);
  font-weight: var(--weight-semibold);
}

@media (prefers-reduced-motion: reduce) {
  .tool-btn,
  .composer__media-remove {
    transition: none;
  }

  .tool-btn:active,
  .composer__media-remove:active {
    transform: none;
  }
}
</style>
