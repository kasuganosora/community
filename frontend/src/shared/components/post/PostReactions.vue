<script setup lang="ts">
import { sessionEpoch } from '@/shared/api/sessionScope'
import { computed, ref, watch } from 'vue'
import { postApi } from '@/domains/post/infrastructure/postApi'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { markStatusEdited } from '@/domains/post/presentation/composables/useStatusActions'
import { emojiUrl } from '@/domains/emoji/domain/emoji'
import type { MastodonEmoji, MastodonEmojiReaction, MastodonStatus } from '@/shared/types/mastodon'
import EmojiPicker from './EmojiPicker.vue'
import EmojiText from '@/shared/components/common/EmojiText.vue'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import ReactionLabel from './ReactionLabel.vue'
const props = defineProps<{ status: MastodonStatus }>()
const auth = useAuthStore()
const pending = ref(false)
const choosing = ref(false)
const error = ref('')
const details = ref(false)
const loadingDetails = ref(false)
const responders = ref<MastodonEmojiReaction[]>([])
const optimistic = ref<MastodonEmojiReaction[] | null>(null)
const reactions = computed(() => optimistic.value ?? props.status.emoji_reactions ?? [])
const unicode = ['👍', '❤️', '😂', '🎉', '😮', '😢']
const total = computed(() => reactions.value.reduce((sum, reaction) => sum + reaction.count, 0))
const addButton = ref<HTMLButtonElement>()
function closePicker() { choosing.value = false; addButton.value?.focus() }
function remoteReaction(reaction: MastodonEmojiReaction) { return !reaction.me && /^:[^:]+@[^:]+:$/.test(reaction.name) }
watch(sessionEpoch, () => { choosing.value = false; details.value = false; responders.value = []; optimistic.value = null; error.value = '' })
watch(() => props.status.id, () => { optimistic.value = null; choosing.value = false; details.value = false; responders.value = []; error.value = '' })
function image(reaction: MastodonEmojiReaction) {
  return reaction.url && !reaction.is_sensitive ? emojiUrl({ shortcode: '', url: reaction.url, static_url: reaction.static_url || '', visible_in_picker: true }) : null
}
async function toggle(name: string) {
  if (!auth.currentUser || pending.value) return
  const id = props.status.id
  const epoch = sessionEpoch.value
  const remove = reactions.value.some(r => r.name === name && r.me)
  const next = reactions.value.map(r => ({ ...r, count: r.count - (r.me ? 1 : 0), me: false })).filter(r => r.count > 0)
  if (!remove) {
    const existing = next.find(r => r.name === name)
    if (existing) { existing.count++; existing.me = true }
    else next.push({ name, count: 1, me: true })
  }
  optimistic.value = next
  pending.value = true
  choosing.value = false
  error.value = ''
  try { const updated = await postApi.react(id, name, remove); if (sessionEpoch.value === epoch) markStatusEdited(updated) }
  catch { if (props.status.id === id) error.value = '无法回应，已恢复原状态：表情可能不可用或受限制，帖子也可能已不可见。' }
  finally { optimistic.value = null; pending.value = false }
}
function choose(emoji: MastodonEmoji) { toggle(`:${emoji.shortcode}:`) }
async function showResponders() {
  const id = props.status.id
  const epoch = sessionEpoch.value
  details.value = true
  responders.value = []
  loadingDetails.value = true
  error.value = ''
  try { const rows = await postApi.reactions(id); if (props.status.id === id && sessionEpoch.value === epoch) responders.value = rows }
  catch { if (props.status.id === id) error.value = '无法读取回应者，帖子可能不再对你可见。' }
  finally { loadingDetails.value = false }
}
</script>
<template>
  <div class="post-reactions" @click.stop @keydown.stop @keydown.esc="closePicker">
    <div class="post-reactions__row" aria-label="表情回应" :aria-busy="pending">
      <button v-for="reaction in reactions" :key="reaction.name" type="button" class="reaction" :class="{ 'reaction--mine': reaction.me, 'reaction--readonly': remoteReaction(reaction) || !auth.currentUser }" :aria-label="`${reaction.name} ${reaction.count} 个回应${reaction.me ? '，撤销我的回应' : ''}`" :title="remoteReaction(reaction) ? '远端表情仅供展示；请管理员复制为本站表情后使用' : reaction.name" :aria-pressed="reaction.me" :disabled="pending || !auth.currentUser || remoteReaction(reaction)" @click="toggle(reaction.name)">
        <span class="reaction__emoji"><img v-if="image(reaction)" :src="image(reaction)!" :alt="reaction.name" referrerpolicy="no-referrer" /><span v-else :class="{ 'reaction__sensitive': reaction.is_sensitive }">{{ reaction.is_sensitive ? '敏感表情' : reaction.name }}</span></span>
        <span class="reaction__count">{{ reaction.count }}</span>
        <svg v-if="reaction.me" class="reaction__check" viewBox="0 0 16 16" aria-hidden="true"><path d="m3.5 8 3 3 6-6" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" /></svg>
      </button>
      <button v-if="auth.currentUser" ref="addButton" type="button" class="reaction reaction--add" :class="{ 'reaction--open': choosing }" aria-label="添加表情回应" :aria-expanded="choosing" :disabled="pending" @click="choosing = !choosing">
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 5v14M5 12h14" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" /></svg>
      </button>
      <button v-if="reactions.length" type="button" class="post-reactions__details" :aria-label="`查看回应者，共 ${total} 个回应`" @click="showResponders">查看回应者<svg viewBox="0 0 16 16" aria-hidden="true"><path d="m6 4 4 4-4 4" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" /></svg></button>
    </div>
    <div v-if="choosing" class="post-reactions__picker">
      <div class="post-reactions__picker-heading"><span>用表情回应</span><span class="post-reactions__hint">选择一个，再点撤销</span></div>
      <div class="reaction-quickbar" aria-label="常用表情">
        <button v-for="emoji in unicode" :key="emoji" type="button" class="reaction-quickbar__item" :class="{ 'reaction-quickbar__item--mine': reactions.some(r => r.name === emoji && r.me) }" :aria-label="`回应 ${emoji}`" :aria-pressed="reactions.some(r => r.name === emoji && r.me)" @click="toggle(emoji)">{{ emoji }}</button>
      </div>
      <EmojiPicker @select="choose" @close="closePicker" />
    </div>
    <p v-if="error && !details" class="post-reactions__error" role="alert">{{ error }}</p>
    <t-dialog v-model:visible="details" header="表情回应" :footer="false" width="min(440px, calc(100vw - 32px))">
      <div class="responders">
        <p v-if="loadingDetails" class="responders__state" role="status"><span class="responders__spinner" aria-hidden="true" />正在加载回应者</p>
        <p v-else-if="error" class="post-reactions__error" role="alert">{{ error }}</p>
        <p v-else-if="!responders.length" class="responders__state">还没有回应，来留下第一个表情吧。</p>
        <section v-for="reaction in responders" :key="reaction.name" class="responders__group">
          <h3 class="responders__heading"><ReactionLabel :reaction="reaction" /><span class="responders__count">{{ reaction.count }}</span><span v-if="reaction.me" class="responders__you">你也回应了</span></h3>
          <ul class="responders__accounts"><li v-for="account in reaction.accounts || []" :key="account.id"><router-link :to="`/@${account.acct}`" class="responders__account" @click="details = false"><UserAvatar :account="account" :size="36" /><span class="responders__identity"><span class="responders__name"><EmojiText :text="account.display_name || account.username" :emojis="account.emojis" /></span><span class="responders__handle">@{{ account.acct }}</span></span></router-link></li></ul>
          <p v-if="reaction.count > (reaction.accounts?.length || 0)" class="responders__more">显示前 {{ reaction.accounts?.length || 0 }} 位回应者</p>
        </section>
      </div>
    </t-dialog>
  </div>
</template>
<style scoped>
.post-reactions { margin-top: var(--space-2); font-size: var(--text-footnote); min-width: 0; }
.post-reactions__row { display: flex; align-items: center; flex-wrap: wrap; gap: 6px; }
.reaction { display: inline-flex; align-items: center; justify-content: center; gap: 6px; min-height: 34px; max-width: 100%; padding: 4px 10px; border: 1px solid transparent; border-radius: 999px; background: var(--color-fill); color: var(--color-label); line-height: 1; transition: background .16s, border-color .16s, transform .16s; cursor: pointer; }
.reaction__emoji { display: inline-flex; align-items: center; min-width: 0; font-size: 19px; line-height: 1.2; overflow: hidden; }
.reaction__emoji img { display: block; width: 22px; height: 22px; object-fit: contain; }
.reaction__emoji>span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.reaction__sensitive { font-size: 11px; color: var(--color-label-secondary); }
.reaction__count { font-size: 12px; font-weight: 650; font-variant-numeric: tabular-nums; }
.reaction__check { width: 12px; height: 12px; flex-shrink: 0; }
.reaction--mine { border-color: var(--color-accent); background: var(--color-accent-soft); color: var(--color-accent-text); }
.reaction--readonly:disabled { opacity: 1; cursor: default; }
.reaction--add { width: 34px; padding: 5px; color: var(--color-label-secondary); background: transparent; border-color: transparent; }
.reaction--add svg { width: 21px; height: 21px; }
.reaction--open { background: var(--color-accent-soft); border-color: var(--color-accent); color: var(--color-accent-text); }
.post-reactions__details { display: inline-flex; align-items: center; gap: 2px; min-height: 34px; padding: 4px 6px; color: var(--color-label-secondary); background: transparent; border: 0; border-radius: 8px; font-size: 11px; cursor: pointer; }
.post-reactions__details svg { width: 13px; height: 13px; }
.post-reactions__picker { margin-top: 10px; padding: 12px; border: 1px solid var(--color-separator); border-radius: 16px; background: var(--color-bg-elevated, var(--color-bg)); max-width: 100%; }
.post-reactions__picker-heading { display: flex; align-items: baseline; justify-content: space-between; flex-wrap: wrap; gap: 4px; margin-bottom: 10px; font-size: 12px; font-weight: 600; }
.post-reactions__hint { font-size: 10px; font-weight: 400; color: var(--color-label-secondary); }
.reaction-quickbar { display: grid; grid-template-columns: repeat(6, minmax(0, 1fr)); gap: 4px; padding: 4px; margin-bottom: 12px; background: var(--color-fill); border-radius: 13px; }
.reaction-quickbar__item { display: flex; align-items: center; justify-content: center; min-width: 0; min-height: 38px; padding: 4px 0; border: 1px solid transparent; border-radius: 10px; background: transparent; font-size: 24px; line-height: 1; cursor: pointer; transition: background .16s, transform .16s; }
.reaction-quickbar__item--mine { border-color: var(--color-accent); background: var(--color-accent-soft); }
.post-reactions button:focus-visible, .responders__account:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 3px; }
.post-reactions__error { padding: 10px 12px; color: var(--color-danger); border-radius: 10px; background: var(--color-fill); font-size: 12px; line-height: 1.6; }
.responders { max-height: min(60vh, 480px); overflow-y: auto; overscroll-behavior: contain; }
.responders__group+.responders__group { margin-top: 16px; padding-top: 16px; border-top: 1px solid var(--color-separator); }
.responders__heading { display: flex; align-items: center; gap: 8px; margin: 0 0 8px; font-size: 17px; font-weight: 500; }
.responders__count { min-width: 22px; padding: 3px 6px; border-radius: 999px; font-size: 11px; font-weight: 600; text-align: center; background: var(--color-fill); color: var(--color-label-secondary); }
.responders__you { margin-left: auto; font-size: 11px; color: var(--color-accent-text); }
.responders__accounts { list-style: none; margin: 0; padding: 0; }
.responders__account { display: flex; align-items: center; gap: 10px; min-width: 0; padding: 8px; border-radius: 12px; text-decoration: none; color: var(--color-label); }
.responders__identity { display: grid; min-width: 0; gap: 2px; }
.responders__name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 13px; font-weight: 600; }
.responders__handle { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 11px; color: var(--color-label-secondary); }
.responders__more { margin: 6px 8px 0; font-size: 11px; color: var(--color-label-secondary); }
.responders__state { display: flex; align-items: center; justify-content: center; gap: 10px; min-height: 100px; color: var(--color-label-secondary); font-size: 13px; }
.responders__spinner { width: 16px; height: 16px; border: 2px solid var(--color-separator); border-top-color: var(--color-accent); border-radius: 50%; animation: reaction-spin .7s linear infinite; }
@keyframes reaction-spin { to { transform: rotate(360deg); } }
@media (hover: hover) { .reaction:not(:disabled):hover { background: var(--color-fill-strong); } .reaction--mine:not(:disabled):hover, .reaction--add:hover { background: var(--color-accent-soft); } .reaction-quickbar__item:hover { background: var(--color-bg); transform: translateY(-2px); } .responders__account:hover, .post-reactions__details:hover { background: var(--color-fill); } }
@media (max-width: 480px) { .reaction { min-height: 38px; } .reaction--add { width: 38px; } .post-reactions__picker { padding: 10px; } .reaction-quickbar__item { font-size: 22px; min-height: 40px; } }
@media (prefers-reduced-motion: reduce) { .reaction, .reaction-quickbar__item { transition: none; } .responders__spinner { animation: none; } }
</style>
