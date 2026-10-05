<script setup lang="ts">
import { ref, computed, reactive, watch, nextTick, onMounted, onBeforeUnmount } from 'vue'
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
  MAX_MEDIA_ATTACHMENTS, MAX_POLL_OPTIONS, MIN_POLL_OPTIONS, POLL_DURATIONS,
  buildCreateParams, composeLength, replyMentions, replyVisibility, validateDraft, visibilityChoices,
  REPLACE_NOTE, REPLACE_ACTION, REPLACE_FAILED, DIRECT_SCOPE,
  type ComposeDraft, type PollDraft,
} from '@/domains/post/domain/compose'
import EmojiPicker from './EmojiPicker.vue'
import EmojiText from '@/shared/components/common/EmojiText.vue'
import { insertShortcode } from '@/domains/emoji/domain/emoji'
import type { MastodonEmoji } from '@/shared/types/mastodon'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'

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
let selection = { start: 0, end: 0 }
function toggleEmojiPicker() {
  const input = composerRoot.value?.querySelector('textarea')
  selection = { start: input?.selectionStart ?? content.value.length, end: input?.selectionEnd ?? content.value.length }
  pickerOpen.value = !pickerOpen.value
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
const quote = ref<MastodonStatus | null>(props.quoteTo ?? null)
watch(() => props.quoteTo, (value) => { quote.value = value ?? null })

function closeVisibilityOnOutside(e: Event) {
  if (!composerRoot.value?.contains(e.target as Node)) visibilityOpen.value = false
}

watch(visibilityOpen, (open) => {
  if (open) document.addEventListener('pointerdown', closeVisibilityOnOutside)
  else document.removeEventListener('pointerdown', closeVisibilityOnOutside)
})

onBeforeUnmount(() => document.removeEventListener('pointerdown', closeVisibilityOnOutside))
const poll = ref<PollDraft | null>(null)
interface MediaItem { key: string; name: string; uploading: boolean; preview: string; attachment?: MastodonAttachment; error?: string }
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
const validationError = computed(() => validateDraft(draft.value, MAX_POST_LENGTH))
const canPost = computed(() => !validationError.value && !posting.value && !uploading.value && !loadingSource.value && !sourceUnavailable.value)
const placeholderText = computed(() => props.placeholder
  || (props.replyTo ? `回复 @${props.replyTo.account.acct}` : '有什么新鲜事？'))
const currentVisibility = computed(() => choices.value.find(v => v.value === draft.value.visibility) ?? choices.value[0]!)
const submitLabel = computed(() => {
  if (posting.value) return isEdit.value ? '正在删除并重发…' : draft.value.visibility === 'direct' ? '正在发送…' : '正在发布…'
  if (isEdit.value) return REPLACE_ACTION
  if (props.replyTo) return '回复'
  return draft.value.visibility === 'direct' ? '发送私信' : '发布'
})
const postingLabel = computed(() => {
  if (isEdit.value) return '正在删除原帖并重新发布，请稍候。'
  if (draft.value.visibility === 'direct') return '正在发送私信…'
  return '正在发布…'
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
    error.value = '无法读取原帖内容，请关闭后重试，原帖尚未更改。'
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
  poll.value = poll.value ? null : { options: ['', ''], expiresIn: 86400, multiple: false }
}

function addPollOption() {
  if (poll.value && poll.value.options.length < MAX_POLL_OPTIONS) poll.value.options.push('')
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
  const room = MAX_MEDIA_ATTACHMENTS - media.length
  if (files.length > room) error.value = `最多只能添加 ${MAX_MEDIA_ATTACHMENTS} 个媒体文件`
  await Promise.all(files.slice(0, room).map(async (file) => {
    const preview = file.type.startsWith('image/') ? URL.createObjectURL(file) : ''
    const item = reactive<MediaItem>({ key: `${Date.now()}-${Math.random()}`, name: file.name, uploading: true, preview })
    media.push(item)
    try {
      item.attachment = await postApi.uploadMedia(file)
    } catch (err) {
      if (item.preview) URL.revokeObjectURL(item.preview)
      item.error = err instanceof Error ? err.message : '上传失败'
      error.value = `${file.name} 上传失败：${item.error}`
      media.splice(media.indexOf(item), 1)
    } finally {
      item.uploading = false
    }
  }))
}

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
    const reason = e instanceof Error && e.message ? `：${e.message}` : '，请重试'
    error.value = `${isEdit.value ? REPLACE_FAILED : draft.value.visibility === 'direct' ? '发送失败，这条私信还没有发出去' : '发布失败'}${reason}`
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
  window.clearTimeout(mentionTimer)
  mentionSeq++
  const label = account.acct.includes('@') ? account.acct : account.username
  const insert = `@${label} `
  const cursor = mentionRange.start + insert.length
  content.value = content.value.slice(0, mentionRange.start) + insert + content.value.slice(mentionRange.end)
  mentions.value = []
  mentionRange = null
  void nextTick(() => {
    const input = composerRoot.value?.querySelector('textarea')
    input?.focus()
    input?.setSelectionRange(cursor, cursor)
  })
}

function onMentionKeydown(e: KeyboardEvent) {
  if (visibilityOpen.value && e.key === 'Escape') {
    e.preventDefault()
    visibilityOpen.value = false
    return
  }
  if (!mentions.value.length) return
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
  <div ref="composerRoot" class="composer" :class="{ 'composer--menu': visibilityOpen }" @keydown.capture="onMentionKeydown" @keydown="onKeydown">
    <p v-if="isEdit" class="composer__replace-notice" role="note">
      {{ REPLACE_NOTE }}
      <span v-if="editStatus?.poll">投票截止时间保持不变。</span>
    </p>
    <p v-else-if="draft.visibility === 'direct'" class="composer__replace-notice" role="note">
      {{ DIRECT_SCOPE }}
    </p>
    <div class="composer__body">
      <UserAvatar
        v-if="authStore.currentUser"
        :account="authStore.currentUser"
        :size="40"
        class="composer__avatar"
      />
      <div class="composer__input-area">
        <input
          v-if="spoilerEnabled"
          v-model="spoilerText"
          class="composer__spoiler"
          type="text"
          dir="auto"
          placeholder="内容警告（例如：剧透）"
          aria-label="内容警告说明"
          maxlength="500"
        />
        <div class="composer__field">
          <t-textarea
            ref="textareaRef"
            v-model="content"
            :placeholder="placeholderText"
            :aria-label="placeholderText"
            :autosize="{ minRows: 1, maxRows: 12 }"
            class="composer__textarea"
            :disabled="loadingSource || posting"
            @keyup="scheduleMentions"
            @click="scheduleMentions"
          />
          <ul v-if="mentions.length" class="composer__mentions" role="listbox" aria-label="提及用户">
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
                  <span class="composer__mention-acct">@{{ account.acct }}</span>
                </span>
              </button>
            </li>
          </ul>
        </div>

        <ul v-if="media.length" class="composer__media" aria-label="已添加的媒体">
          <li v-for="(m, i) in media" :key="m.key" class="composer__media-item">
            <img v-if="mediaPreview(m)" :src="mediaPreview(m)" alt="" />
            <span v-else class="composer__media-name">{{ m.uploading ? '上传中…' : m.name }}</span>
            <t-loading v-if="m.uploading" size="small" class="composer__media-spinner" />
            <button
              v-else
              type="button"
              class="composer__media-remove"
              :aria-label="`移除 ${m.name}`"
              @click="removeMedia(i)"
            >
              <t-icon name="close" :size="14" />
            </button>
          </li>
        </ul>

        <fieldset v-if="poll" class="composer__poll">
          <legend class="composer__poll-legend">投票</legend>
          <div v-for="(_, i) in poll.options" :key="i" class="composer__poll-option">
            <input
              v-model="poll.options[i]"
              type="text"
              :placeholder="`选项 ${i + 1}`"
              :aria-label="`投票选项 ${i + 1}`"
              maxlength="50"
            />
            <button
              v-if="poll.options.length > MIN_POLL_OPTIONS"
              type="button"
              class="tool-btn"
              :aria-label="`删除选项 ${i + 1}`"
              @click="removePollOption(i)"
            >
              <t-icon name="close" :size="14" />
            </button>
          </div>
          <div class="composer__poll-settings">
            <button
              v-if="poll.options.length < MAX_POLL_OPTIONS"
              type="button"
              class="composer__link-btn"
              @click="addPollOption"
            >
              添加选项
            </button>
            <label class="composer__poll-check">
              <input v-model="poll.multiple" type="checkbox" />
              允许多选
            </label>
            <label class="composer__poll-duration">
              <span class="sr-only">投票时长</span>
              <select v-model.number="poll.expiresIn" aria-label="投票时长">
                <option v-for="d in POLL_DURATIONS" :key="d.value" :value="d.value">{{ d.label }}</option>
              </select>
            </label>
          </div>
        </fieldset>

        <EmojiPicker v-if="pickerOpen" @select="insertEmoji" @close="pickerOpen = false" />
        <div v-if="quote && !isEdit" class="composer__quote">
          <span class="composer__quote-who">引用 @{{ quote.account.acct }}</span>
          <p>{{ quote.spoiler_text ? `内容警告：${quote.spoiler_text}` : (stripHtml(quote.content || '').replace(/\s+/g, ' ').trim() || '（这条引用没有文字）') }}</p>
          <button type="button" class="composer__link-btn" @click="quote = null">移除引用</button>
        </div>
        <p v-if="posting" class="composer__status" role="status">{{ postingLabel }}</p>
        <div v-else-if="error" class="composer__error" role="alert">
          <span>{{ error }}</span>
          <button v-if="!sourceUnavailable" type="button" class="composer__retry" :disabled="!canPost" @click="handlePost">重试</button>
        </div>
        <div class="composer__toolbar">
          <div class="composer__tools">
            <button type="button" class="tool-btn" aria-label="自定义表情" :aria-expanded="pickerOpen" :disabled="posting || loadingSource" @click="toggleEmojiPicker">☺</button>
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
                aria-label="添加图片或视频"
                title="添加图片或视频"
                :disabled="!!poll || media.length >= MAX_MEDIA_ATTACHMENTS"
                @click="pickFiles"
              >
                <t-icon name="image" :size="18" />
              </button>
              <button
                type="button"
                class="tool-btn"
                :class="{ 'tool-btn--active': !!poll }"
                :aria-pressed="!!poll"
                aria-label="投票"
                title="投票"
                :disabled="media.length > 0"
                @click="togglePoll"
              >
                <t-icon name="chart-bar" :size="18" />
              </button>
            </template>
            <button
              type="button"
              class="tool-btn tool-btn--text"
              :class="{ 'tool-btn--active': spoilerEnabled }"
              :aria-pressed="spoilerEnabled"
              aria-label="内容警告"
              title="内容警告"
              @click="toggleSpoiler"
            >
              CW
            </button>
            <div v-if="!isEdit && choices.length > 1" class="composer__visibility">
              <button
                type="button"
                class="composer__visibility-btn"
                :aria-expanded="visibilityOpen"
                aria-haspopup="listbox"
                aria-label="可见范围"
                :title="currentVisibility.label"
                @click="visibilityOpen = !visibilityOpen"
              >
                <t-icon :name="currentVisibility.icon" :size="16" />
                <span>{{ currentVisibility.label }}</span>
              </button>
              <ul v-if="visibilityOpen" class="composer__visibility-menu" role="listbox" aria-label="可见范围">
                <li v-for="v in choices" :key="v.value">
                  <button
                    type="button"
                    class="composer__visibility-option"
                    :class="{ 'is-selected': v.value === visibility }"
                    role="option"
                    :aria-selected="v.value === visibility"
                    @click="visibility = v.value; visibilityOpen = false"
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
            <t-button
              v-if="isEdit"
              variant="text"
              shape="round"
              size="small"
              @click="emit('cancel')"
            >
              取消
            </t-button>
            <t-button
              theme="primary"
              shape="round"
              size="small"
              :disabled="!canPost"
              :loading="posting"
              @click="handlePost"
            >
              {{ submitLabel }}
            </t-button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.composer {
  padding: var(--space-3) var(--gutter);
  box-shadow: inset 0 calc(-1 * var(--hairline)) 0 var(--color-separator);
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

.composer__body {
  display: flex;
  gap: var(--space-3);
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

/* The text field is the canvas: no box, no focus ring — the caret and the
   enabled Post button are the feedback. */
.composer__textarea :deep(.t-textarea__inner),
.composer__textarea :deep(.t-textarea__inner:hover),
.composer__textarea :deep(.t-textarea__inner:focus),
.composer__textarea :deep(.t-textarea__inner.t-is-focused) {
  background: transparent !important;
  border: none !important;
  box-shadow: none !important;
  outline: none !important;
  color: var(--color-label) !important;
  font-size: var(--text-title-3) !important;
  line-height: var(--leading-title-3) !important;
  letter-spacing: 0;
  unicode-bidi: plaintext;
  padding: var(--space-2) 0 !important;
  resize: none !important;
}

.composer__textarea :deep(.t-textarea__inner:lang(zh)) {
  line-height: 1.45 !important;
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
  padding-top: var(--space-2);
  margin-top: var(--space-2);
  box-shadow: inset 0 var(--hairline) 0 var(--color-separator);
}

.composer__tools {
  display: flex;
  margin-left: calc(-1 * var(--space-2));
}

.tool-btn {
  display: grid;
  place-items: center;
  width: 2.5rem;
  height: 2.5rem;
  border-radius: var(--radius-full);
  color: var(--color-accent-text);
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
    background: var(--color-accent-soft);
  }
}

.tool-btn:active {
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

.composer__right :deep(.t-button) {
  min-width: 4.5rem;
  font-weight: var(--weight-semibold);
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

.tool-btn--text {
  width: auto;
  min-width: 2.5rem;
  padding: 0 var(--space-2);
  font-size: var(--text-footnote);
  font-weight: var(--weight-semibold);
}

.tool-btn--active {
  background: var(--color-accent-soft);
}

/* Phones: the toolbar runs the full width under the avatar so the tools,
   counter and button never crowd each other. */
@media (max-width: 43.74rem) {
  .composer__toolbar {
    margin-left: calc(-40px - var(--space-3));
  }
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
  min-height: 2rem;
  color: var(--color-accent-text);
  font-weight: var(--weight-medium);
}

.composer__link-btn:active {
  opacity: 0.6;
}

.composer__poll-check {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
}

.composer__poll-check input {
  width: 1.125rem;
  height: 1.125rem;
  accent-color: var(--color-accent);
}

.composer__poll-duration select {
  min-height: 2rem;
  border: none;
  background: #fff;
  color: #1c1c1e;
  font: inherit;
  font-size: var(--text-subhead);
  font-weight: var(--weight-medium);
  cursor: pointer;
  color-scheme: light;
}

.composer__visibility {
  position: relative;
  display: inline-flex;
  align-items: center;
}

.composer__visibility--locked {
  min-height: 2.5rem;
  padding: 0 var(--space-2);
  gap: var(--space-1);
  border-radius: var(--radius-full);
  color: var(--color-accent-text);
  font-size: var(--text-subhead);
  font-weight: var(--weight-medium);
}

.composer__visibility-btn {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  min-height: 2.5rem;
  padding: 0 var(--space-1);
  border-radius: var(--radius-full);
  color: var(--color-accent-text);
  font-size: var(--text-subhead);
  font-weight: var(--weight-medium);
}

.composer__visibility-btn[aria-expanded='true'] {
  background: var(--color-accent-soft);
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
  left: 0;
  min-width: 16rem;
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
  min-height: 2.5rem;
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
</style>
