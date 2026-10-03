<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import RichText from '@/shared/components/common/RichText.vue'
import PostMedia from '@/shared/components/post/PostMedia.vue'
import { safeHttpLink } from '@/shared/utils/links'
import { sessionEpoch } from '@/shared/api/sessionScope'
import type { MastodonAttachment } from '@/shared/types/mastodon'
import { archiveApi, type ArchivedPost } from '../../infrastructure/archiveApi'
const items = ref<ArchivedPost[]>([]), next = ref<string | null>(null), loading = ref(false), error = ref('')
const attachments = ref<Record<string, MastodonAttachment>>({}), loadingMedia = ref<string[]>([])
let generation = 0, controller = new AbortController()
function reset() { generation++; controller.abort(); controller = new AbortController(); items.value = []; next.value = null; loading.value = false; error.value = ''; attachments.value = {}; loadingMedia.value = [] }
function fail(e: unknown) { const status = (e as { response?: { status?: number } } | null)?.response?.status; if (status === 401 || status === 403) reset(); error.value = e instanceof Error ? e.message : '读取私有备份失败' }
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
    if (!url) throw new Error('附件地址不可用')
    attachments.value[id] = { ...media, url, preview_url: preview || '' }
  } catch (e) { if (version === generation) fail(e) } finally { if (version === generation) loadingMedia.value = loadingMedia.value.filter(x => x !== id) }
}
watch(sessionEpoch, reset, { flush: 'sync' })
onMounted(() => { void load() }); onUnmounted(reset)
</script>
<template>
  <section class="private-archive" aria-label="私有备份内容">
    <h2>私有备份内容</h2><p>仅你可见。这些是保留的源记录，未冒充原作者重新发帖，也未向联邦广播。原作者标识不会被当作本站账号。</p>
    <button :disabled="loading" @click="load()">刷新私有备份</button><p v-if="error" role="alert">{{ error }}</p><p v-if="loading" role="status">读取中…</p><p v-if="!loading && !items.length">暂无私有备份内容</p>
    <article v-for="post in items" :key="post.id">
      <p>原作者标识：{{ post.author_id }}</p><p>源记录：{{ post.source_id }} · {{ post.created_at }}</p>
      <details :open="!post.spoiler">
        <summary>{{ post.spoiler || '查看备份正文' }}</summary><RichText v-if="post.html" :html="post.html" /><p v-else class="archive-text">{{ post.text }}</p>
        <ul><li v-for="media in post.media" :key="media.source_id">{{ media.name || media.source_id }}<template v-if="media.available && media.id"><button v-if="!attachments[media.id]" :disabled="loadingMedia.includes(media.id)" @click="showMedia(media.id)">显示附件</button><PostMedia v-else :attachments="[attachments[media.id]!]" /></template><span v-else>（附件未包含在可用备份中）</span></li></ul>
      </details>
      <a v-if="safeHttpLink(post.source_uri)" :href="safeHttpLink(post.source_uri)!" target="_blank" rel="noopener noreferrer" referrerpolicy="no-referrer">在源站查看</a>
    </article>
    <button v-if="next" :disabled="loading" @click="load(true)">加载更多备份</button>
  </section>
</template>
<style scoped>
.private-archive{display:flex;flex-direction:column;gap:var(--space-3)}article{border-top:1px solid var(--color-separator);padding:var(--space-3) 0;overflow-wrap:anywhere}.archive-text{white-space:pre-wrap}button,summary{min-height:44px;cursor:pointer}a{color:var(--color-accent-text)}
</style>
