<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useEmojiStore } from '@/domains/emoji/presentation/stores/emojiStore'
import { emojiUrl } from '@/domains/emoji/domain/emoji'
import type { MastodonEmoji } from '@/shared/types/mastodon'
const emit = defineEmits<{ select: [emoji: MastodonEmoji]; close: [] }>()
const store = useEmojiStore()
const query = ref('')
const category = ref('')
const sensitive = ref(false)
const available = computed(() => store.emojis.filter(e => e.visible_in_picker && !e.disabled))
const categories = computed(() => [...new Set(available.value.map(e => e.category || '未分类'))].sort())
const matches = computed(() => available.value.filter(e => {
  const q = query.value.trim().toLocaleLowerCase()
  return (!category.value || (e.category || '未分类') === category.value)
    && (!q || [e.shortcode, ...(e.aliases || [])].some(s => s.toLocaleLowerCase().includes(q)))
}))
onMounted(() => store.load())
</script>
<template>
  <section class="emoji-picker" aria-label="自定义表情选择器" @keydown.esc.stop="emit('close')">
    <div class="emoji-picker__filters">
      <input v-model="query" type="search" aria-label="搜索表情名称或别名" placeholder="搜索名称或别名" />
      <select v-model="category" aria-label="表情分类"><option value="">全部分类</option><option v-for="c in categories" :key="c">{{ c }}</option></select>
      <button type="button" aria-label="刷新表情列表" :disabled="store.loading" @click="store.load(true)">刷新</button>
      <button type="button" aria-label="关闭表情选择器" @click="emit('close')">×</button>
    </div>
    <label><input v-model="sensitive" type="checkbox" />显示敏感表情</label>
    <p v-if="store.loading" role="status">加载表情…</p>
    <p v-else-if="store.error" role="alert">{{ store.error }} <button type="button" @click="store.load(true)">重试</button></p>
    <p v-else-if="!matches.length">没有匹配的表情</p>
    <div v-else class="emoji-picker__grid">
      <button v-for="emoji in matches" :key="emoji.shortcode" type="button" :aria-label="`插入 :${emoji.shortcode}:${emoji.local_only ? '（仅本站显示）' : ''}`" :title="`${emoji.shortcode}${emoji.license ? ' · ' + emoji.license : ''}`" :disabled="!!emoji.is_sensitive && !sensitive" @click="emit('select', emoji)">
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
.emoji-picker__filters input, .emoji-picker__filters select { min-width: 0; width: 100%; box-sizing: border-box; }
.emoji-picker__filters input, .emoji-picker__filters select { color: var(--color-label); background: var(--color-bg-row); border: 1px solid var(--color-separator); border-radius: var(--radius-sm); padding: var(--space-1); }
.emoji-picker__grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(5rem, 1fr)); gap: var(--space-1); max-height: 15rem; overflow: auto; margin-top: var(--space-2); }
.emoji-picker__grid button { display: flex; flex-direction: column; align-items: center; gap: var(--space-1); padding: var(--space-2); min-width: 0; border-radius: var(--radius-sm); }
.emoji-picker__grid button:hover { background: var(--color-fill); }
.emoji-picker__grid img { width: 2rem; height: 2rem; object-fit: contain; }
.emoji-picker__grid small { max-width: 100%; overflow-wrap: anywhere; }
.emoji-picker__grid button:disabled { opacity: .6; }
</style>
