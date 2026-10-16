<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useEmojiStore } from '@/domains/emoji/presentation/stores/emojiStore'
import { emojiUrl, readRecentEmoji, rememberRecentEmoji } from '@/domains/emoji/domain/emoji'
import AppIcon from '@/shared/components/common/AppIcon.vue'
import type { MastodonEmoji } from '@/shared/types/mastodon'
import { t } from '@/shared/i18n'
const emit = defineEmits<{ select: [emoji: MastodonEmoji]; close: [] }>()
const store = useEmojiStore()
const query = ref('')
const category = ref('')
const sensitive = ref(false)
const recentCodes = ref(readRecentEmoji())
const grid = ref<HTMLElement>()
const searchInput = ref<HTMLInputElement>()
const highlighted = ref<MastodonEmoji | null>(null)
watch([query, category, sensitive], () => { highlighted.value = null })
const available = computed(() => store.emojis.filter(e => e.visible_in_picker && !e.disabled))
const categories = computed(() => [...new Set(available.value.map(e => e.category || t('shared.EmojiPicker.s26')))].sort())
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
    return (category.value === '__recent' || !category.value || (e.category || t('shared.EmojiPicker.s26')) === category.value)
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
  const cols = Math.max(1, Math.floor((width + 6) / 62))
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
onMounted(() => { void store.load(); searchInput.value?.focus({ preventScroll: true }) })
</script>
<template>
  <section class="emoji-picker" :aria-label="$t('shared.EmojiPicker.s1')" @keydown="onKeydown">
    <header class="emoji-picker__header">
      <div class="emoji-picker__heading"><span class="emoji-picker__mark"><AppIcon name="happy" /></span><div><strong>{{ $t('shared.EmojiPicker.s2') }}</strong><span>{{ $t('shared.EmojiPicker.s3') }}</span></div></div>
      <div class="emoji-picker__tools"><button type="button" class="icon-button" :aria-label="$t('shared.EmojiPicker.s4')" :disabled="store.loading" @click="store.load(true)"><AppIcon name="refresh" /></button><button type="button" class="icon-button" :aria-label="$t('shared.EmojiPicker.s5')" @click="emit('close')"><AppIcon name="close" /></button></div>
    </header>
    <div class="emoji-picker__search"><AppIcon name="search" /><input ref="searchInput" v-model="query" type="search" :aria-label="$t('shared.EmojiPicker.s6')" :placeholder="$t('shared.EmojiPicker.s7')" /><button v-if="query" type="button" class="icon-button" :aria-label="$t('shared.EmojiPicker.s8')" @click="query = ''; searchInput?.focus()"><AppIcon name="close" /></button></div>
    <div class="emoji-picker__filters">
      <div class="emoji-picker__cats" :aria-label="$t('shared.EmojiPicker.s9')">
        <button type="button" :aria-pressed="category !== '__recent'" @click="category = ''">{{ $t('shared.EmojiPicker.s10') }}</button>
        <button v-if="recentEmojis.length" type="button" :aria-pressed="category === '__recent'" @click="category = '__recent'"><AppIcon name="time" />{{ $t('shared.EmojiPicker.s11') }}</button>
      </div>
      <select :value="category === '__recent' ? '' : category" :aria-label="$t('shared.EmojiPicker.s12')" @change="category = ($event.target as HTMLSelectElement).value"><option value="">{{ $t('shared.EmojiPicker.s13') }}</option><option v-for="c in categories" :key="c">{{ c }}</option></select>
    </div>
    <div v-if="store.loading" class="emoji-picker__empty" role="status"><AppIcon name="refresh" /><strong>{{ $t('shared.EmojiPicker.s14') }}</strong><span>{{ $t('shared.EmojiPicker.s15') }}</span></div>
    <div v-else-if="store.error" class="emoji-picker__empty" role="alert"><AppIcon name="error-circle" /><strong>{{ $t('shared.EmojiPicker.s16') }}</strong><span>{{ store.error }}</span><button type="button" class="retry-button" @click="store.load(true)">{{ $t('shared.EmojiPicker.s17') }}</button></div>
    <div v-else-if="!matches.length" class="emoji-picker__empty"><AppIcon name="search" /><strong>{{ category === '__recent' ? $t('shared.EmojiPicker.s18') : $t('shared.EmojiPicker.s19') }}</strong><span>{{ query ? $t('shared.EmojiPicker.s20') : $t('shared.EmojiPicker.s21') }}</span></div>
    <div v-else ref="grid" class="emoji-picker__grid">
      <button v-for="emoji in matches" :key="emoji.shortcode" type="button" :aria-label="$t('shared.EmojiPicker.s23', [emoji.shortcode, emoji.local_only ? $t('shared.EmojiPicker.s22') : ''])" :title="`${emoji.shortcode}${emoji.license ? ' · ' + emoji.license : ''}`" :disabled="!!emoji.is_sensitive && !sensitive" @mouseenter="highlighted = emoji" @focus="highlighted = emoji" @click="choose(emoji)">
        <img v-if="(!emoji.is_sensitive || sensitive) && emojiUrl(emoji)" :src="emojiUrl(emoji)!" :alt="`:${emoji.shortcode}:`" loading="lazy" referrerpolicy="no-referrer" />
        <AppIcon v-else :name="emoji.is_sensitive ? 'lock-on' : 'happy'" />
        <small>{{ emoji.is_sensitive && !sensitive ? $t('shared.EmojiPicker.s24') : emoji.shortcode }}</small><i v-if="emoji.local_only" aria-hidden="true" class="local-dot" />
      </button>
    </div>
    <footer class="emoji-picker__footer">
      <div class="emoji-picker__preview" aria-live="polite"><strong>{{ highlighted ? `:${highlighted.shortcode}:` : $t('shared.EmojiPicker.s25') }}</strong><span>{{ highlighted ? [highlighted.category || $t('shared.EmojiPicker.s26'), highlighted.local_only ? $t('shared.EmojiPicker.s27') : '', highlighted.license || ''].filter(Boolean).join(' · ') : $t('shared.EmojiPicker.s28', [matches.length]) }}</span></div>
      <label class="emoji-picker__sensitive"><input v-model="sensitive" type="checkbox" /><span>{{ $t('shared.EmojiPicker.s29') }}</span></label>
    </footer>
  </section>
</template>
<style scoped>
.emoji-picker { margin:12px 0; padding:16px; min-width:0; color:var(--color-label); background:var(--color-bg-row); border:1px solid var(--color-separator); border-radius:20px; box-shadow:0 8px 28px rgb(0 0 0 / 5%); font-size:13px; }
.emoji-picker__header,.emoji-picker__heading,.emoji-picker__tools { display:flex; align-items:center; gap:10px; }
.emoji-picker__header { justify-content:space-between; margin-bottom:14px; }
.emoji-picker__heading strong { display:block; font-size:14px; }.emoji-picker__heading span:not(.emoji-picker__mark) { display:block; margin-top:3px; font-size:11px; color:var(--color-label-secondary); }
.emoji-picker__mark { display:grid; place-items:center; width:36px; height:36px; border-radius:12px; background:var(--color-accent-soft); color:var(--color-accent-text); font-size:22px; }
.icon-button { display:grid; place-items:center; flex-shrink:0; width:32px; height:32px; border-radius:10px; color:var(--color-label-secondary); font-size:18px; }
.emoji-picker__tools { gap:2px; }.emoji-picker button { border:0; cursor:pointer; }.emoji-picker button:disabled { opacity:.45; cursor:default; }
.emoji-picker__search { display:flex; align-items:center; gap:9px; padding:4px 10px; border:1px solid transparent; background:var(--color-bg-grouped); border-radius:12px; color:var(--color-label-secondary); }
.emoji-picker__search:focus-within { border-color:var(--color-accent-text); box-shadow:0 0 0 3px var(--color-accent-soft); }
.emoji-picker__search input { width:100%; min-width:0; min-height:34px; padding:0; border:0; outline:0; background:transparent; color:var(--color-label); font:inherit; }.emoji-picker__search input::-webkit-search-cancel-button { display:none; }
.emoji-picker__filters { display:flex; align-items:center; justify-content:space-between; gap:8px; margin:12px 0; }.emoji-picker__cats { display:flex; gap:4px; flex-shrink:0; }
.emoji-picker__cats button { display:flex; align-items:center; justify-content:center; gap:4px; min-height:34px; padding:0 12px; border-radius:9px; color:var(--color-label-secondary); background:transparent; font:inherit; }
.emoji-picker__cats button[aria-pressed='true'] { background:var(--color-accent-soft); color:var(--color-accent-text); font-weight:600; }
.emoji-picker__filters select { min-width:0; max-width:45%; min-height:34px; border:1px solid var(--color-separator); border-radius:9px; padding:0 6px; color:var(--color-label-secondary); background:var(--color-bg-row); font:inherit; }
.emoji-picker__grid { display:grid; grid-template-columns:repeat(auto-fill,minmax(56px,1fr)); align-content:start; gap:6px; max-height:240px; min-height:116px; overflow:auto; overscroll-behavior:contain; padding:3px; margin:0 -3px; }
.emoji-picker__grid button { position:relative; display:flex; flex-direction:column; align-items:center; justify-content:center; gap:6px; min-width:0; min-height:68px; padding:7px 3px; border-radius:12px; background:transparent; color:var(--color-label-secondary); transition:background .15s,transform .15s; }.emoji-picker__grid img { width:32px; height:32px; object-fit:contain; }.emoji-picker__grid :deep(svg) { font-size:26px; }.emoji-picker__grid small { max-width:100%; overflow:hidden; white-space:nowrap; text-overflow:ellipsis; font-size:10px; }.local-dot { position:absolute; right:7px; top:7px; width:5px; height:5px; border-radius:50%; background:var(--color-accent-text); }
.emoji-picker button:focus-visible,.emoji-picker select:focus-visible { outline:2px solid var(--color-accent-text); outline-offset:1px; }.emoji-picker__grid button:active:not(:disabled) { transform:scale(.94); }
@media(hover:hover) { .icon-button:hover,.emoji-picker__grid button:hover:not(:disabled) { background:var(--color-accent-soft); color:var(--color-accent-text); } }
.emoji-picker__empty { display:flex; flex-direction:column; align-items:center; justify-content:center; gap:9px; min-height:156px; text-align:center; color:var(--color-label-secondary); }.emoji-picker__empty>:deep(svg) { font-size:28px; opacity:.6; }.emoji-picker__empty span { font-size:12px; }.retry-button { padding:8px 14px; border-radius:9px; color:var(--color-accent-text); background:var(--color-accent-soft); }
.emoji-picker__footer { display:flex; align-items:center; justify-content:space-between; flex-wrap:wrap; gap:12px; margin-top:12px; padding-top:12px; border-top:1px solid var(--color-separator); }.emoji-picker__preview { min-width:0; flex:1 1 160px; }.emoji-picker__preview strong { display:block; font-size:12px; overflow-wrap:anywhere; }.emoji-picker__preview>span { display:block; margin-top:4px; font-size:11px; color:var(--color-label-secondary); overflow-wrap:anywhere; }.emoji-picker__sensitive { display:flex; align-items:center; gap:6px; min-height:32px; font-size:11px; color:var(--color-label-secondary); cursor:pointer; }.emoji-picker__sensitive input { accent-color:var(--color-accent-text); }
@media(max-width:400px) { .emoji-picker { padding:12px; border-radius:16px; }.emoji-picker__heading { gap:7px; min-width:0; }.emoji-picker__heading>div { min-width:0; }.emoji-picker__heading span:not(.emoji-picker__mark) { white-space:nowrap; overflow:hidden; text-overflow:ellipsis; }.emoji-picker__mark { width:30px; height:30px; }.emoji-picker__heading strong { font-size:13px; }.emoji-picker__cats button { padding:0 9px; } }
@media(prefers-reduced-motion:reduce) { .emoji-picker__grid button { transition:none; } }
</style>
