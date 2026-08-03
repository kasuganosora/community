<script setup lang="ts">
import { ref, computed, onUnmounted } from 'vue'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { MAX_POST_LENGTH } from '@/shared/utils/constants'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'

const emit = defineEmits<{
  posted: [content: string]
}>()

const authStore = useAuthStore()
const content = ref('')
const posting = ref(false)
const error = ref<string | null>(null)

const charCount = computed(() => content.value.length)
const isOverLimit = computed(() => charCount.value > MAX_POST_LENGTH)
const canPost = computed(() => content.value.trim().length > 0 && !isOverLimit.value && !posting.value)

const POST_TIMEOUT = 30000 // 30s safety timeout
let postTimer: ReturnType<typeof setTimeout> | null = null

function clearPostTimer() {
  if (postTimer) {
    clearTimeout(postTimer)
    postTimer = null
  }
}

async function handlePost() {
  if (!canPost.value || posting.value) return
  posting.value = true
  error.value = null
  // Emit and keep posting=true until parent confirms via @posted event handler
  // Parent is responsible for calling onPostSuccess/onPostError
  emit('posted', content.value)
  // Safety: if parent doesn't call onPostSuccess/onPostError within timeout,
  // reset posting state to avoid permanently stuck button
  clearPostTimer()
  postTimer = setTimeout(() => {
    if (posting.value) {
      posting.value = false
      error.value = '请求超时，请重试'
    }
    postTimer = null
  }, POST_TIMEOUT)
}

/** Called by parent on successful post to reset the composer */
function onPostSuccess() {
  clearPostTimer()
  content.value = ''
  posting.value = false
  error.value = null
}

/** Called by parent on post failure to show error and keep content */
function onPostError(msg?: string) {
  clearPostTimer()
  posting.value = false
  error.value = msg || '发布失败，请重试'
}

onUnmounted(clearPostTimer)

defineExpose({ onPostSuccess, onPostError })
</script>

<template>
  <div class="composer">
    <div class="composer__body">
      <UserAvatar
        v-if="authStore.currentUser"
        :account="authStore.currentUser"
        :size="40"
        class="composer__avatar"
      />
      <div class="composer__input-area">
        <t-textarea
          v-model="content"
          placeholder="What is happening?!"
          :autosize="{ minRows: 1, maxRows: 12 }"
          class="composer__textarea"
        />
        <div v-if="error" class="composer__error">{{ error }}</div>
        <div class="composer__toolbar">
          <div class="composer__tools">
            <button class="tool-btn"><t-icon name="image" :size="18" /></button>
            <button class="tool-btn"><t-icon name="gif" :size="18" /></button>
            <button class="tool-btn"><t-icon name="smile" :size="18" /></button>
            <button class="tool-btn"><t-icon name="location" :size="18" /></button>
          </div>
          <div class="composer__right">
            <span
              v-if="charCount > 0"
              class="composer__count"
              :class="{ 'composer__count--over': isOverLimit }"
            >
              {{ charCount }}/{{ MAX_POST_LENGTH }}
            </span>
            <t-button
              theme="primary"
              shape="round"
              size="small"
              :disabled="!canPost"
              :loading="posting"
              @click="handlePost"
            >
              Post
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

.composer__error {
  color: var(--danger, #e53e3e);
  font-size: var(--font-sm, 0.875rem);
  padding: var(--space-1, 0.25rem) 0;
}
</style>
