import { ref } from 'vue'
import { defineStore } from 'pinia'
import type { MastodonEmoji } from '@/shared/types/mastodon'
import { t } from '@/shared/i18n'
import { readCachedEmojiCatalogue, writeCachedEmojiCatalogue } from '../../domain/emojiCatalogue'
import { loadCustomEmojiCatalogue } from '../emojiCatalogueClient'

export const useEmojiStore = defineStore('custom-emojis', () => {
  const cached = readCachedEmojiCatalogue()
  const emojis = ref<MastodonEmoji[]>(cached?.emojis ?? [])
  const loading = ref(false)
  const error = ref<string | null>(null)
  let revision: string | null = cached?.revision ?? null
  let pending: Promise<void> | null = null
  let generation = 0
  // One successful check per store is enough for reaction rows. The picker
  // passes force so each open revalidates; If-None-Match still skips the body.
  let checked = false
  function load(force = false): Promise<void> {
    // A refresh must not join an in-flight request: that request may never
    // settle, and its finally would otherwise clear a newer attempt.
    if (pending && !force) return pending
    if (checked && !force) return Promise.resolve()
    const mine = ++generation
    // A warm catalogue paints immediately. The conditional request runs
    // behind it and only replaces the list when the server revision changes.
    const hadCatalogue = emojis.value.length > 0 || revision !== null
    if (!hadCatalogue) {
      loading.value = true
      error.value = null
    }
    const sent = revision
    const run = Promise.resolve()
      .then(() => loadCustomEmojiCatalogue(sent))
      .then((result) => {
        if (mine !== generation) return
        checked = true
        error.value = null
        if (result.unchanged) return
        emojis.value = result.emojis
        revision = result.revision
        writeCachedEmojiCatalogue({ revision: result.revision, emojis: result.emojis })
      })
      .catch(() => {
        if (mine !== generation) return
        if (!hadCatalogue) error.value = t('emoji.emojiStore.s1')
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
