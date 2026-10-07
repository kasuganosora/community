<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useEmojiStore } from '@/domains/emoji/presentation/stores/emojiStore'
import { emojiUrl, readRecentEmoji, rememberRecentEmoji } from '@/domains/emoji/domain/emoji'
import type { MastodonEmoji } from '@/shared/types/mastodon'
const emit = defineEmits<{ select: [emoji: MastodonEmoji]; close: [] }>()
const store = useEmojiStore()
const query = ref('')
const category = ref('')
const sensitive = ref(false)
const recentCodes = ref(readRecentEmoji())
const grid = ref<HTMLElement>()
const available = computed(() => store.emojis.filter(e => e.visible_in_picker && !e.disabled))
const categories = computed(() => [...new Set(available.value.map(e => e.category || '未分类'))].sort())
const recentEmojis = computed(() => {
  const byCode = new Map(available.value.map(emoji => [emoji.shortcode, emoji]))
  return recentCodes.value.flatMap(code => {
    const emoji = byCode.get(code)
    return emoji ? [emoji] : []
  })
})
const matches = computed(() => {
  const source = category.value === '__recent' ? recentEmojis.value : available.value
  const q = query.value.trim().toLocaleLowerCase()
  return source.filter(e => {
    return (category.value === '__recent' || !category.value || (e.category || '未分类') === category.value)
      && (!q || [e.shortcode, ...(e.aliases || [])].some(s => s.toLocaleLowerCase().includes(q)))
  })
})
function choose(emoji: MastodonEmoji) {
  if (emoji.is_sensitive && !sensitive.value) return
  recentCodes.value = rememberRecentEmoji(emoji.shortcode, recentCodes.value)
  emit('select', emoji)
}
function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    emit('close')
    return
  }
  const target = event.target as HTMLElement | null
  const typing = !!target?.matches('input, select, textarea')
  if (typing && event.key !== 'ArrowDown') return
  const buttons = Array.from(grid.value?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? [])
  if (!buttons.length) return
  const current = buttons.findIndex(button => button === document.activeElement)
  const width = grid.value?.clientWidth || 320
  const cols = Math.max(1, Math.floor(width / 80))
  let next: number
  if (event.key === 'ArrowDown') next = current < 0 ? 0 : Math.min(buttons.length - 1, current + cols)
  else if (event.key === 'ArrowUp') next = Math.max(0, current - cols)
  else if (event.key === 'ArrowRight') next = Math.min(buttons.length - 1, current < 0 ? 0 : current + 1)
  else if (event.key === 'ArrowLeft') next = Math.max(0, current < 0 ? 0 : current - 1)
  else if (event.key === 'Home') next = 0
  else if (event.key === 'End') next = buttons.length - 1
  else return
  event.preventDefault()
  buttons[next]?.focus()
}
onMounted(() => store.load())
</script>
<template>
  <section class="emoji-picker" aria-label="自定义表情选择器" @keydown="onKeydown">
    <div class="emoji-picker__filters">
      <input v-model="query" type="search" aria-label="搜索表情名称或别名" placeholder="搜索名称或别名" />
      <select :value="category === '__recent' ? '' : category" aria-label="表情分类" @change="category = ($event.target as HTMLSelectElement).value">
        <option value="">全部分类</option>
        <option v-for="c in categories" :key="c">{{ c }}</option>
      </select>
      <button type="button" aria-label="刷新表情列表" :disabled="store.loading" @click="store.load(true)">刷新</button>
      <button type="button" aria-label="关闭表情选择器" @click="emit('close')">×</button>
    </div>
    <div class="emoji-picker__cats" role="tablist" aria-label="按分类浏览">
      <button type="button" role="tab" :aria-selected="category === ''" @click="category = ''">全部</button>
      <button v-if="recentEmojis.length" type="button" role="tab" :aria-selected="category === '__recent'" @click="category = '__recent'">最近使用</button>
      <button v-for="c in categories" :key="c" type="button" role="tab" :aria-selected="category === c" @click="category = c">{{ c }}</button>
    </div>
    <label><input v-model="sensitive" type="checkbox" />显示敏感表情</label>
    <p v-if="store.loading" role="status">加载表情…</p>
    <p v-else-if="store.error" role="alert">{{ store.error }} <button type="button" @click="store.load(true)">重试</button></p>
    <p v-else-if="category === '__recent' && !matches.length">还没有最近使用的表情</p>
    <p v-else-if="!matches.length">没有匹配的表情</p>
    <div v-else ref="grid" class="emoji-picker__grid">
      <button v-for="emoji in matches" :key="emoji.shortcode" type="button" :aria-label="`插入 :${emoji.shortcode}:${emoji.local_only ? '（仅本站显示）' : ''}`" :title="`${emoji.shortcode}${emoji.license ? ' · ' + emoji.license : ''}`" :disabled="!!emoji.is_sensitive && !sensitive" @click="choose(emoji)">
        <img v-if="(!emoji.is_sensitive || sensitive) && emojiUrl(emoji)" :src="emojiUrl(emoji)!" :alt="`:${emoji.shortcode}:`" loading="lazy" referrerpolicy="no-referrer" />
        <span v-else>{{ emoji.is_sensitive ? '敏感表情' : `:${emoji.shortcode}:` }}</span>
        <small>{{ emoji.shortcode }}</small><small v-if="emoji.local_only">仅本站显示</small>
      </button>
    </div>
  </section>
</template>
<style scoped>
.emoji-picker { margin: var(--space-2) 0; padding: var(--space-3); background: var(--color-bg-grouped); border-radius: var(--radius-md); font-size: var(--text-footnote); }
.emoji-picker__filters { display: grid; grid-template-columns: minmax(0, 1fr) auto auto; gap: var(--space-2); margin-bottom: var(--space-2); }
.emoji-picker__filters input { grid-column: 1 / -1; }
.emoji-picker__filters button { min-width: 2rem; min-height: 2rem; padding: var(--space-1); }
.emoji-picker__filters input, .emoji-picker__filters select { min-width: 0; width: 100%; box-sizing: border-box; color: var(--color-label); background: var(--color-bg-row); border: 1px solid var(--color-separator); border-radius: var(--radius-sm); padding: var(--space-1); }
.emoji-picker__cats { display: flex; gap: var(--space-1); overflow-x: auto; margin-bottom: var(--space-2); padding-bottom: var(--space-1); }
.emoji-picker__cats button { flex: 0 0 auto; min-height: 2rem; padding: 0 var(--space-3); border-radius: var(--radius-full); background: var(--color-fill); }
.emoji-picker__cats button[aria-selected='true'] { background: var(--color-accent-soft); color: var(--color-accent-text); }
.emoji-picker__grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(5rem, 1fr)); gap: var(--space-1); max-height: 15rem; overflow: auto; margin-top: var(--space-2); }
.emoji-picker__grid button { display: flex; flex-direction: column; align-items: center; gap: var(--space-1); padding: var(--space-2); min-width: 0; border-radius: var(--radius-sm); }
.emoji-picker__grid button:focus-visible { background: var(--color-fill); }
@media (hover: hover) and (pointer: fine) {
  .emoji-picker__grid button:hover { background: var(--color-fill); }
}
.emoji-picker__grid img { width: 2rem; height: 2rem; object-fit: contain; }
.emoji-picker__grid small { max-width: 100%; overflow-wrap: anywhere; }
.emoji-picker__grid button:disabled { opacity: .6; }
</style>
