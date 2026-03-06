<script setup lang="ts">
import { ref, computed } from 'vue'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { MAX_POST_LENGTH } from '@/shared/utils/constants'

const emit = defineEmits<{
  posted: [content: string]
}>()

const authStore = useAuthStore()
const content = ref('')
const posting = ref(false)

const charCount = computed(() => content.value.length)
const isOverLimit = computed(() => charCount.value > MAX_POST_LENGTH)
const canPost = computed(() => content.value.trim().length > 0 && !isOverLimit.value && !posting.value)

async function handlePost() {
  if (!canPost.value) return
  posting.value = true
  try {
    emit('posted', content.value)
    content.value = ''
  } finally {
    posting.value = false
  }
}
</script>

<template>
  <div class="composer">
    <div class="composer__body">
      <t-avatar
        :size="'40px'"
        :image="authStore.currentUser?.avatar || undefined"
        shape="circle"
        class="composer__avatar"
      >
        {{ authStore.currentUser?.display_name?.charAt(0) || 'U' }}
      </t-avatar>
      <div class="composer__input-area">
        <t-textarea
          v-model="content"
          placeholder="What is happening?!"
          :autosize="{ minRows: 1, maxRows: 12 }"
          class="composer__textarea"
        />
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
  padding: 12px 16px;
  border-bottom: 1px solid var(--border-color);
}

.composer__body {
  display: flex;
  gap: 12px;
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
  padding: 8px 0 !important;
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
  padding-top: 12px;
  border-top: 1px solid var(--border-color);
  margin-top: 12px;
}

.composer__tools {
  display: flex;
  gap: 4px;
}

.tool-btn {
  padding: 8px;
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
  gap: 12px;
}

.composer__count {
  font-size: var(--font-xs);
  color: var(--text-secondary);
}

.composer__count--over {
  color: var(--danger);
}
</style>
