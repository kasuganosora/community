<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount, watch, computed } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { formatCount } from '@/shared/utils/format'
import { discoveryApi, isDiscoveryReset, type TagsPage, type TagItem } from '../../infrastructure/discoveryApi'
import { discoveryInvalidation, invalidateDiscovery } from '../composables/discoveryState'

const props = defineProps<{ compact?: boolean }>()
const auth = useAuthStore()
const windowSize = ref<'24h' | '7d'>('24h')
const items = ref<TagItem[]>([]), page = ref<TagsPage | null>(null)
const loading = ref(false), error = ref(''), reset = ref(false)
const title = computed(() => page.value?.kind === 'recent' ? '近期话题' : '热点话题')
const rangeLabel = computed(() => `全局（含跨站） · 过去${windowSize.value === '24h' ? ' 24 小时' : ' 7 天'}`)
let version = 0, controller: AbortController | undefined
async function load(more = false) {
  if (more && (loading.value || !page.value?.next_cursor || reset.value)) return
  const mine = ++version, epoch = sessionEpoch.value
  controller?.abort(); controller = new AbortController()
  loading.value = true; error.value = ''; reset.value = false
  const cursor = more ? page.value?.next_cursor ?? undefined : undefined
  if (!more) { items.value = []; page.value = null }
  try {
    const value = await discoveryApi.tags({ window: windowSize.value, scope: 'federated', limit: props.compact ? 5 : 20, cursor }, controller.signal)
    if (mine !== version || epoch !== sessionEpoch.value) return
    if (value.disabled) { page.value = value; items.value = []; return }
    if (cursor && value.next_cursor === cursor) throw new Error('话题分页没有前进，请刷新后重试')
    const names = new Set(items.value.map(item => item.name))
    items.value = [...items.value, ...value.items.filter(item => {
      if (names.has(item.name)) return false
      names.add(item.name)
      return true
    })].slice(0, 200)
    if (items.value.length >= 200) value.next_cursor = null
    page.value = value
  } catch (e) {
    if (mine !== version || epoch !== sessionEpoch.value) return
    if (isDiscoveryReset(e)) reset.value = true
    else error.value = e instanceof Error ? e.message : '无法加载话题，请重试'
  } finally { if (mine === version) loading.value = false }
}
async function hideTag(tag: TagItem) {
  const epoch = sessionEpoch.value
  try {
    const value = await discoveryApi.addFeedback({ action: 'hide_tag', target_kind: 'tag', target_key: tag.name })
    if (epoch !== sessionEpoch.value) return
    items.value = items.value.filter(item => item.name !== tag.name)
    invalidateDiscovery(value)
    MessagePlugin.success('已停止推荐这个话题，可在设置中撤销')
  } catch (e) { MessagePlugin.error(e instanceof Error ? e.message : '未能保存，请重试') }
}
function resume() { if (!document.hidden) void load() }
watch([windowSize, sessionEpoch, discoveryInvalidation], () => { void load() }, { flush: 'sync' })
onMounted(() => { void load(); document.addEventListener('visibilitychange', resume) })
onBeforeUnmount(() => { ++version; controller?.abort(); document.removeEventListener('visibilitychange', resume) })
</script>

<template>
  <section v-if="!compact || !page?.disabled" class="discovery-tags" :class="{ 'discovery-tags--compact': compact }" :aria-busy="loading">
    <header class="discovery-tags__header"><h2>{{ title }}</h2><p v-if="!compact">{{ rangeLabel }}</p></header>
    <div v-if="!compact" class="discovery-tags__filters">
      <label>统计窗口<select v-model="windowSize"><option value="24h">过去 24 小时</option><option value="7d">过去 7 天</option></select></label>
    </div>
    <p v-if="page?.disabled" class="discovery-tags__message" role="status">本站暂未开放热点话题。</p>
    <p v-else-if="page?.kind === 'recent' && !compact" class="discovery-tags__message">当前参与人数不足以形成热点榜，以下是窗口内的近期公开话题。</p>
    <ul v-if="items.length" class="discovery-tags__list">
      <li v-for="tag in items" :key="tag.name">
        <router-link :to="`/tags/${encodeURIComponent(tag.name.replace(/^#/, ''))}`">
          <strong>#{{ tag.name }}</strong>
          <span>{{ formatCount(tag.posts_count) }} 条公开帖 · {{ formatCount(tag.authors_count) }} 位作者</span>
          <small v-if="!compact && tag.growth_state === 'rising'">近期参与上升</small>
          <small v-else-if="!compact && tag.growth_state === 'no_previous'">上一窗口暂无可比数据</small>
        </router-link>
        <button v-if="auth.currentUser && !compact" type="button" class="discovery-tags__hide" :aria-label="`停止推荐话题 ${tag.name}`" @click="hideTag(tag)"><t-icon name="close" :size="16" /></button>
      </li>
    </ul>
    <p v-if="error" class="discovery-tags__message discovery-tags__error" role="alert">{{ error }} <button type="button" :disabled="loading" @click="load(!!items.length)">重试</button></p>
    <p v-if="loading" class="discovery-tags__message" role="status">正在读取话题…</p>
    <p v-else-if="!items.length && !error && !page?.disabled && !reset" class="discovery-tags__message">最近暂无可展示的公开话题。</p>
    <p v-if="reset" class="discovery-tags__message" role="status">榜单已更新，请刷新后继续。 <button type="button" @click="load()">刷新榜单</button></p>
    <div v-if="!compact && page && !page.disabled" class="discovery-tags__footer">
      <p>包含本站及已接收的跨站公开内容。</p>
      <p v-if="!page.coverage_complete">统计受处理预算限制，仅反映本站本轮已观察的内容。</p>
      <p v-if="page.as_of">统计截至 {{ new Date(page.as_of).toLocaleString() }}</p>
      <button v-if="page.next_cursor && !reset" type="button" class="btn btn--secondary btn--sm" :disabled="loading" @click="load(true)">继续加载</button>
      <button v-else type="button" class="btn btn--ghost btn--sm" :disabled="loading" @click="load()">刷新话题</button>
    </div>
    <router-link v-if="compact" class="discovery-tags__more" to="/explore/topics">查看全部话题</router-link>
  </section>
</template>

<style scoped>
.discovery-tags { min-width: 0; }
.discovery-tags--compact { background: var(--color-bg-grouped); border-radius: var(--radius-lg); overflow: hidden; }
.discovery-tags__header, .discovery-tags__filters, .discovery-tags__message, .discovery-tags__footer { padding: var(--space-3) var(--space-4); }
.discovery-tags__header h2 { font-size: var(--text-title-3); font-weight: var(--weight-bold); }
.discovery-tags__header p, .discovery-tags__message, .discovery-tags__footer { color: var(--color-label-secondary); font-size: var(--text-footnote); line-height: 1.6; }
.discovery-tags__header p { margin-top: var(--space-1); }
.discovery-tags__filters { display: flex; flex-wrap: wrap; gap: var(--space-3); border-bottom: 1px solid var(--color-separator); }
.discovery-tags__filters label { flex: 1; min-width: 10rem; display: grid; gap: .5rem; font-size: var(--text-footnote); }
.discovery-tags__filters select { width: 100%; min-height: var(--hit-target); border: 1px solid var(--color-separator); border-radius: var(--radius-md); background: var(--color-bg-row); color: var(--color-label); padding: .5rem .75rem; font: inherit; font-size: 1rem; }
.discovery-tags__list { list-style: none; padding: 0; }
.discovery-tags__list li { display: flex; align-items: center; padding: 0 var(--space-4); gap: .5rem; }
.discovery-tags__list a { display: block; flex: 1; min-width: 0; color: var(--color-label); padding: var(--space-3) 0; text-decoration: none; }
.discovery-tags__list strong { display: block; font-size: var(--text-body); overflow-wrap: anywhere; }
.discovery-tags__list span, .discovery-tags__list small { display: block; margin-top: .25rem; color: var(--color-label-secondary); font-size: var(--text-footnote); }
.discovery-tags__hide { width: var(--hit-target); height: var(--hit-target); flex: none; color: var(--color-label-secondary); }
.discovery-tags__hide:active:not(:focus-visible) { background: var(--color-pressed); border-radius: var(--radius-full); }
.discovery-tags__more { display: flex; align-items: center; min-height: var(--hit-target); padding: var(--space-2) var(--space-4) var(--space-3); color: var(--color-accent-text); }
.discovery-tags__message button { color: var(--color-accent-text); min-height: var(--hit-target); }
.discovery-tags__error { color: var(--color-danger); }
.discovery-tags__footer { display: flex; flex-wrap: wrap; gap: .5rem 1rem; align-items: center; }
.discovery-tags__footer p { flex-basis: 100%; }
.discovery-tags__footer button { min-height: var(--hit-target); }
.discovery-tags :is(a, button, select):focus-visible { outline: 2px solid var(--color-accent); outline-offset: 3px; }
@media (hover: hover) and (pointer: fine) { .discovery-tags__list li:hover { background: var(--color-hover); } }
</style>
