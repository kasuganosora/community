<script setup lang="ts">
/**
 * Body of a status: content warning, text, media and poll. Shared by the
 * timeline card and the status detail page.
 */
import { ref, computed, watch } from 'vue'
import type { MastodonPoll, MastodonStatus } from '@/shared/types/mastodon'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import RichText from '@/shared/components/common/RichText.vue'
import PostMedia from './PostMedia.vue'
import PostPoll from './PostPoll.vue'

const props = defineProps<{
  status: MastodonStatus
  textClass?: string
}>()

const authStore = useAuthStore()
const cwOpen = ref(false)
const mediaRevealed = ref(false)
const pollOverride = ref<MastodonPoll | null>(null)

watch(() => props.status.id, () => {
  cwOpen.value = false
  mediaRevealed.value = false
  pollOverride.value = null
})

const hasCw = computed(() => !!props.status.spoiler_text)
const bodyVisible = computed(() => !hasCw.value || cwOpen.value)
const poll = computed(() => pollOverride.value ?? props.status.poll)
const isOwn = computed(() => !!authStore.currentUser && authStore.currentUser.id === props.status.account.id)
// Media marked sensitive without a CW still needs a click to reveal.
const mediaHidden = computed(() => props.status.sensitive && !hasCw.value && !mediaRevealed.value)
const contentId = computed(() => `status-content-${props.status.id}-${Math.random().toString(36).slice(2, 6)}`)
</script>

<template>
  <div class="post-content">
    <div v-if="hasCw" class="post-content__cw">
      <span class="post-content__cw-text">{{ status.spoiler_text }}</span>
      <button
        type="button"
        class="post-content__cw-toggle"
        :aria-expanded="cwOpen"
        :aria-controls="contentId"
        @click.stop="cwOpen = !cwOpen"
      >
        {{ cwOpen ? '收起' : '显示内容' }}
      </button>
    </div>
    <div v-show="bodyVisible" :id="contentId">
      <RichText :html="status.content" :class="textClass" @click.stop />
      <div v-if="status.media_attachments?.length" class="post-content__media">
        <PostMedia v-if="!mediaHidden" :attachments="status.media_attachments" />
        <button
          v-else
          type="button"
          class="post-content__sensitive"
          @click.stop="mediaRevealed = true"
        >
          敏感内容，点击查看
        </button>
      </div>
      <PostPoll v-if="poll" :poll="poll" :own="isOwn" @updated="pollOverride = $event" />
    </div>
  </div>
</template>

<style scoped>
.post-content__cw {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  margin: var(--space-1) 0;
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-sm);
  background: var(--color-fill);
}

.post-content__cw-text {
  flex: 1;
  min-width: 0;
  font-weight: var(--weight-medium);
  color: var(--color-label);
}

.post-content__cw-toggle {
  min-height: 1.75rem;
  padding: 0 var(--space-3);
  border-radius: var(--radius-full);
  background: var(--color-bg-row);
  color: var(--color-accent-text);
  font-size: var(--text-footnote);
  font-weight: var(--weight-semibold);
  box-shadow: var(--shadow-1);
  transition: transform var(--duration-release) var(--ease-out);
}

.post-content__cw-toggle:active {
  transform: scale(var(--press-scale));
  transition-duration: var(--duration-press);
}

.post-content__sensitive {
  width: 100%;
  margin-top: var(--space-2);
  padding: var(--space-8) var(--space-4);
  border-radius: var(--radius-lg);
  background: var(--color-fill);
  color: var(--color-label-secondary);
  font-size: var(--text-subhead);
  font-weight: var(--weight-medium);
  transition: background-color var(--duration-release) var(--ease-out);
}

.post-content__sensitive:active {
  background: var(--color-fill-strong);
  transition-duration: 0s;
}
</style>
