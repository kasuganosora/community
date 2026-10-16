import { ref } from 'vue'
import { defineStore } from 'pinia'
import client from '@/shared/api/client'
import type { MastodonEmoji } from '@/shared/types/mastodon'

import { t } from '@/shared/i18n'
export const useEmojiStore = defineStore('custom-emojis', () => {
  const emojis = ref<MastodonEmoji[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)
  let loaded = false
  let pending: Promise<void> | null = null
  function load(force = false): Promise<void> {
    if (pending) return pending
    if (loaded && !force) return Promise.resolve()
    loading.value = true
    error.value = null
    pending = client.get<MastodonEmoji[]>('/api/v1/custom_emojis').then(({ data }) => {
      emojis.value = data
      loaded = true
    }).catch(() => { error.value = t('emoji.emojiStore.s1') }).finally(() => { loading.value = false; pending = null })
    return pending
  }
  return { emojis, loading, error, load }
})
