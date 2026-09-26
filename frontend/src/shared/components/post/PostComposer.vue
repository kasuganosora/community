<script setup lang="ts">
import { ref, computed, reactive, watch, nextTick, onMounted } from 'vue'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { postApi } from '@/domains/post/infrastructure/postApi'
import { MAX_POST_LENGTH } from '@/shared/utils/constants'
import type { MastodonAttachment, MastodonStatus, Visibility } from '@/shared/types/mastodon'
import {
  MAX_MEDIA_ATTACHMENTS, MAX_POLL_OPTIONS, MIN_POLL_OPTIONS, POLL_DURATIONS, VISIBILITY_OPTIONS,
  buildCreateParams, composeLength, replyMentions, replyVisibility, validateDraft,
  type ComposeDraft, type PollDraft,
} from '@/domains/post/domain/compose'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'

const props = defineProps<{
  /** Compose a reply to this status (prefills mentions and visibility). */
  replyTo?: MastodonStatus | null
  /** Edit this (own) status instead of creating a new one. */
  editStatus?: MastodonStatus | null
  placeholder?: string
  autofocus?: boolean
}>()

const emit = defineEmits<{
  /** The created (or, in edit mode, updated) status returned by the API. */
  posted: [status: MastodonStatus]
  cancel: []
}>()

const authStore = useAuthStore()
const uid = Math.random().toString(36).slice(2, 8)

const content = ref('')
const spoilerEnabled = ref(false)
const spoilerText = ref('')
const visibility = ref<Visibility>('public')
const poll = ref<PollDraft | null>(null)
interface MediaItem { key: string; name: string; uploading: boolean; attachment?: MastodonAttachment; error?: string }
const media = reactive<MediaItem[]>([])
const posting = ref(false)
const error = ref<string | null>(null)
const fileInput = ref<HTMLInputElement>()
const textareaRef = ref<{ focus?: () => void } | null>(null)

const isEdit = computed(() => !!props.editStatus)
const mediaIds = computed(() => media.filter(m => m.attachment).map(m => m.attachment!.id))
const uploading = computed(() => media.some(m => m.uploading))

const draft = computed<ComposeDraft>(() => ({
  text: content.value,
  spoilerEnabled: spoilerEnabled.value,
  spoilerText: spoilerText.value,
  visibility: visibility.value,
  poll: poll.value,
  mediaIds: mediaIds.value,
  inReplyToId: props.replyTo?.id,
}))
const charCount = computed(() => composeLength(draft.value))
const isOverLimit = computed(() => charCount.value > MAX_POST_LENGTH)
const validationError = computed(() => validateDraft(draft.value, MAX_POST_LENGTH))
const canPost = computed(() => !validationError.value && !posting.value && !uploading.value)
const placeholderText = computed(() => props.placeholder
  || (props.replyTo ? `回复 @${props.replyTo.account.acct}` : '有什么新鲜事？'))
const currentVisibility = computed(() => VISIBILITY_OPTIONS.find(v => v.value === visibility.value) ?? VISIBILITY_OPTIONS[0]!)

function resetForReply() {
  content.value = props.replyTo ? replyMentions(props.replyTo, authStore.currentUser?.acct) : ''
  visibility.value = replyVisibility(props.replyTo?.visibility)
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
    // Fall back to the rendered content as plain text.
    const div = document.createElement('div')
    div.innerHTML = s.content
    content.value = div.textContent || ''
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
    const item = reactive<MediaItem>({ key: `${Date.now()}-${Math.random()}`, name: file.name, uploading: true })
    media.push(item)
    try {
      item.attachment = await postApi.uploadMedia(file)
    } catch (err) {
      item.error = err instanceof Error ? err.message : '上传失败'
      error.value = `${file.name} 上传失败：${item.error}`
      media.splice(media.indexOf(item), 1)
    } finally {
      item.uploading = false
    }
  }))
}

function removeMedia(i: number) {
  media.splice(i, 1)
}

function reset() {
  content.value = ''
  spoilerEnabled.value = false
  spoilerText.value = ''
  poll.value = null
  media.splice(0, media.length)
  error.value = null
  resetForReply()
}

async function handlePost() {
  if (posting.value) return
  if (validationError.value) {
    error.value = validationError.value
    return
  }
  posting.value = true
  error.value = null
  try {
    let status: MastodonStatus
    if (props.editStatus) {
      status = await postApi.updateStatus(props.editStatus.id, {
        status: content.value,
        spoiler_text: spoilerEnabled.value ? spoilerText.value.trim() : '',
        sensitive: spoilerEnabled.value && !!spoilerText.value.trim(),
      })
    } else {
      status = await postApi.create(buildCreateParams(draft.value))
    }
    emit('posted', status)
    if (!props.editStatus) reset()
  } catch (e) {
    console.error('Failed to post status:', e)
    error.value = (e instanceof Error && e.message) ? `发布失败：${e.message}` : '发布失败，请重试'
  } finally {
    posting.value = false
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
  <div class="composer" @keydown="onKeydown">
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
          placeholder="内容警告（例如：剧透）"
          aria-label="内容警告说明"
          maxlength="500"
        />
        <t-textarea
          ref="textareaRef"
          v-model="content"
          :placeholder="placeholderText"
          :aria-label="placeholderText"
          :autosize="{ minRows: 1, maxRows: 12 }"
          class="composer__textarea"
        />

        <ul v-if="media.length" class="composer__media" aria-label="已添加的媒体">
          <li v-for="(m, i) in media" :key="m.key" class="composer__media-item">
            <img v-if="m.attachment?.type === 'image'" :src="m.attachment.preview_url || m.attachment.url" :alt="m.name" />
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

        <div v-if="error" class="composer__error" role="alert">{{ error }}</div>
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
            <label v-if="!isEdit" class="composer__visibility" :title="currentVisibility.label">
              <t-icon :name="currentVisibility.icon" :size="16" />
              <select v-model="visibility" aria-label="可见范围">
                <option v-for="v in VISIBILITY_OPTIONS" :key="v.value" :value="v.value">{{ v.label }}</option>
              </select>
            </label>
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
              {{ isEdit ? '保存' : (replyTo ? '回复' : '发布') }}
            </t-button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.composer {
  padding: var(--space-3) var(--space-4);
  border-bottom: 1px solid var(--border-color);
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

.composer__textarea :deep(.t-textarea__inner) {
  background: transparent !important;
  border: none !important;
  color: var(--text-primary) !important;
  font-size: var(--font-xl) !important;
  padding: var(--space-2) 0 !important;
  resize: none !important;
}

.composer__textarea :deep(.t-textarea__inner::placeholder) {
  color: var(--text-tertiary) !important;
}

.composer__textarea :deep(.t-input__wrap) {
  border: none !important;
}

.composer__toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding-top: var(--space-3);
  border-top: 1px solid var(--border-color);
  margin-top: var(--space-3);
}

.composer__tools {
  display: flex;
  gap: var(--space-1);
}

.tool-btn {
  padding: var(--space-2);
  border-radius: var(--radius-full);
  color: var(--accent-blue);
  transition: background-color var(--transition-fast);
}

.tool-btn:hover {
  background: rgba(29, 155, 240, 0.1);
}

.composer__right {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

.composer__count {
  font-size: var(--font-xs);
  color: var(--text-secondary);
}

.composer__count--over {
  color: var(--danger);
}

.tool-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.tool-btn--text {
  font-size: var(--font-xs);
  font-weight: var(--font-bold, 700);
  min-width: 34px;
}

.tool-btn--active {
  background: var(--accent-blue-light, rgba(29, 155, 240, 0.1));
}

.composer__spoiler,
.composer__poll-option input {
  width: 100%;
  background: transparent;
  color: var(--text-primary);
  border: 1px solid var(--border-color);
  border-radius: var(--radius-md, 8px);
  padding: var(--space-2) var(--space-3);
  font: inherit;
}

.composer__spoiler {
  margin-bottom: var(--space-2);
}

.composer__media {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(96px, 1fr));
  gap: var(--space-2);
  margin: var(--space-2) 0 0;
  padding: 0;
  list-style: none;
}

.composer__media-item {
  position: relative;
  aspect-ratio: 1;
  border-radius: var(--radius-md, 8px);
  overflow: hidden;
  background: var(--bg-secondary);
  display: flex;
  align-items: center;
  justify-content: center;
}

.composer__media-item img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.composer__media-name {
  font-size: var(--font-xs);
  color: var(--text-secondary);
  padding: var(--space-1);
  word-break: break-all;
}

.composer__media-remove {
  position: absolute;
  top: 4px;
  right: 4px;
  width: 24px;
  height: 24px;
  border-radius: var(--radius-full);
  background: rgba(0, 0, 0, 0.7);
  color: #fff;
  display: flex;
  align-items: center;
  justify-content: center;
}

.composer__media-spinner {
  position: absolute;
}

.composer__poll {
  border: 1px solid var(--border-color);
  border-radius: var(--radius-md, 8px);
  padding: var(--space-2) var(--space-3);
  margin: var(--space-2) 0 0;
}

.composer__poll-legend {
  font-size: var(--font-xs);
  color: var(--text-secondary);
  padding: 0 var(--space-1);
}

.composer__poll-option {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  margin-bottom: var(--space-2);
}

.composer__poll-settings {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-3);
  font-size: var(--font-sm);
  color: var(--text-secondary);
}

.composer__link-btn {
  color: var(--accent-blue);
}

.composer__poll-check {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
}

.composer__poll-duration select,
.composer__visibility select {
  background: transparent;
  color: var(--accent-blue);
  border: none;
  font: inherit;
  font-size: var(--font-sm);
  cursor: pointer;
}

.composer__visibility {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  color: var(--accent-blue);
  padding: 0 var(--space-1);
}

.composer__visibility select {
  max-width: 7.5em;
}

.composer__visibility option,
.composer__poll-duration option {
  color: initial;
}

.composer__error {
  color: var(--danger, #e53e3e);
  font-size: var(--font-sm, 0.875rem);
  padding: var(--space-1, 0.25rem) 0;
}
</style>
