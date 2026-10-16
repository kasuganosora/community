<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import RichText from '@/shared/components/common/RichText.vue'
import PostMedia from '@/shared/components/post/PostMedia.vue'
import { safeHttpLink } from '@/shared/utils/links'
import { sessionEpoch } from '@/shared/api/sessionScope'
import type { MastodonAttachment } from '@/shared/types/mastodon'
import { archiveApi, type ArchivedPost } from '../../infrastructure/archiveApi'
import { t } from '@/shared/i18n'
const items = ref<ArchivedPost[]>([]), next = ref<string | null>(null), loading = ref(false), error = ref('')
const attachments = ref<Record<string, MastodonAttachment>>({}), loadingMedia = ref<string[]>([])
let generation = 0, controller = new AbortController()
function reset() { generation++; controller.abort(); controller = new AbortController(); items.value = []; next.value = null; loading.value = false; error.value = ''; attachments.value = {}; loadingMedia.value = [] }
function fail(e: unknown) { const status = (e as { response?: { status?: number } } | null)?.response?.status; if (status === 401 || status === 403) reset(); error.value = e instanceof Error ? e.message : t('data.PrivateArchiveList.s13') }
async function load(more = false) {
  if (loading.value || (more && !next.value)) return
  if (!more) reset()
  loading.value = true; error.value = ''; const version = generation
  try { const result = await archiveApi.list(more ? next.value! : undefined, controller.signal); if (version !== generation) return
    const seen = new Set(items.value.map(p => p.id)); items.value.push(...result.items.filter(p => !seen.has(p.id) && !!seen.add(p.id))); next.value = result.next_max_id
  } catch (e) { if (version === generation) fail(e) } finally { if (version === generation) loading.value = false }
}
async function showMedia(id: string) {
  if (loadingMedia.value.includes(id)) return
  const version = generation; loadingMedia.value.push(id); error.value = ''
  try { const media = await archiveApi.media(id, controller.signal); if (version !== generation) return
    const url = safeHttpLink(media.url), preview = safeHttpLink(media.preview_url)
    if (!url) throw new Error(t('data.PrivateArchiveList.s14'))
    attachments.value[id] = { ...media, url, preview_url: preview || '' }
  } catch (e) { if (version === generation) fail(e) } finally { if (version === generation) loadingMedia.value = loadingMedia.value.filter(x => x !== id) }
}
watch(sessionEpoch, reset, { flush: 'sync' })
onMounted(() => { void load() }); onUnmounted(reset)
</script>
<template>
  <section class="private-archive" :aria-label="$t('data.PrivateArchiveList.s1')">
    <h2>{{ $t('data.PrivateArchiveList.s1') }}</h2><p>{{ $t('data.PrivateArchiveList.s2') }}</p>
    <button :disabled="loading" @click="load()">{{ $t('data.PrivateArchiveList.s3') }}</button><p v-if="error" role="alert">{{ error }}</p><p v-if="loading" role="status">{{ $t('data.PrivateArchiveList.s4') }}</p><p v-if="!loading && !error && !items.length">{{ $t('data.PrivateArchiveList.s5') }}</p>
    <article v-for="post in items" :key="post.id">
      <p>{{ $t('data.PrivateArchiveList.s6', [post.author_id]) }}</p><p>{{ $t('data.PrivateArchiveList.s7', [post.source_id, post.created_at]) }}</p>
      <details :open="!post.spoiler">
        <summary>{{ post.spoiler || $t('data.PrivateArchiveList.s8') }}</summary><RichText v-if="post.html" :html="post.html" /><p v-else class="archive-text">{{ post.text }}</p>
        <ul><li v-for="media in post.media" :key="media.source_id">{{ media.name || media.source_id }}<template v-if="media.available && media.id"><button v-if="!attachments[media.id]" :disabled="loadingMedia.includes(media.id)" @click="showMedia(media.id)">{{ $t('data.PrivateArchiveList.s9') }}</button><PostMedia v-else :attachments="[attachments[media.id]!]" /></template><span v-else>{{ $t('data.PrivateArchiveList.s10') }}</span></li></ul>
      </details>
      <a v-if="safeHttpLink(post.source_uri)" :href="safeHttpLink(post.source_uri)!" target="_blank" rel="noopener noreferrer" referrerpolicy="no-referrer">{{ $t('data.PrivateArchiveList.s11') }}</a>
    </article>
    <button v-if="next" :disabled="loading" @click="load(true)">{{ $t('data.PrivateArchiveList.s12') }}</button>
  </section>
</template>
<style scoped>
.private-archive{display:flex;flex-direction:column;gap:var(--space-3)}article{border-top:1px solid var(--color-separator);padding:var(--space-3) 0;overflow-wrap:anywhere}.archive-text{white-space:pre-wrap}button,summary{min-height:44px;cursor:pointer}a{color:var(--color-accent-text)}
</style>
