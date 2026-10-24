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
  let generation = 0
  function load(force = false): Promise<void> {
    // A refresh must not join an in-flight request: that request may never
    // settle, and its finally would otherwise clear a newer attempt.
    if (pending && !force) return pending
    if (loaded && !force) return Promise.resolve()
    const mine = ++generation
    loading.value = true
    error.value = null
    const run = Promise.resolve()
      .then(() => client.get<MastodonEmoji[]>('/api/v1/custom_emojis'))
      .then(({ data }) => {
        if (mine !== generation) return
        if (!Array.isArray(data)) throw new Error('invalid emoji catalogue')
        emojis.value = data
        loaded = true
      })
      .catch(() => {
        if (mine !== generation) return
        error.value = t('emoji.emojiStore.s1')
      })
      .finally(() => {
        if (mine !== generation) return
        loading.value = false
        pending = null
      })
    pending = run
    return run
  }
  return { emojis, loading, error, load }
})
