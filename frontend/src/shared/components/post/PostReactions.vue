<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { postApi } from '@/domains/post/infrastructure/postApi'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { markStatusEdited } from '@/domains/post/presentation/composables/useStatusActions'
import { emojiUrl } from '@/domains/emoji/domain/emoji'
import type { MastodonEmoji, MastodonEmojiReaction, MastodonStatus } from '@/shared/types/mastodon'
import EmojiPicker from './EmojiPicker.vue'
import EmojiText from '@/shared/components/common/EmojiText.vue'
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
watch(() => props.status.id, () => { optimistic.value = null; choosing.value = false; details.value = false; responders.value = []; error.value = '' })
function image(reaction: MastodonEmojiReaction) {
  return reaction.url && !reaction.is_sensitive ? emojiUrl({ shortcode: '', url: reaction.url, static_url: reaction.static_url || '', visible_in_picker: true }) : null
}
async function toggle(name: string) {
  if (!auth.currentUser || pending.value) return
  const id = props.status.id
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
  try { markStatusEdited(await postApi.react(id, name, remove)) }
  catch { if (props.status.id === id) error.value = '无法回应，已恢复原状态：表情可能不可用或受限制，帖子也可能已不可见。' }
  finally { optimistic.value = null; pending.value = false }
}
function choose(emoji: MastodonEmoji) { toggle(`:${emoji.shortcode}:`) }
async function showResponders() {
  const id = props.status.id
  details.value = true
  responders.value = []
  loadingDetails.value = true
  error.value = ''
  try { const rows = await postApi.reactions(id); if (props.status.id === id) responders.value = rows }
  catch { if (props.status.id === id) error.value = '无法读取回应者，帖子可能不再对你可见。' }
  finally { loadingDetails.value = false }
}
</script>
<template>
  <div class="post-reactions" @click.stop @keydown.stop>
    <div class="post-reactions__row">
      <button v-for="reaction in reactions" :key="reaction.name" type="button" class="reaction" :class="{ 'reaction--mine': reaction.me }" :aria-label="`${reaction.name} ${reaction.count} 个回应${reaction.me ? '，撤销我的回应' : ''}`" :title="!reaction.me && /^:[^:]+@[^:]+:$/.test(reaction.name) ? '远端表情仅供展示；请管理员复制为本站表情后使用' : reaction.name" :aria-pressed="reaction.me" :disabled="pending || !auth.currentUser || (!reaction.me && /^:[^:]+@[^:]+:$/.test(reaction.name))" @click="toggle(reaction.name)">
        <img v-if="image(reaction)" :src="image(reaction)!" :alt="reaction.name" referrerpolicy="no-referrer" /><span v-else>{{ reaction.is_sensitive ? '敏感表情' : reaction.name }}</span><span>{{ reaction.count }}</span>
      </button>
      <button v-if="auth.currentUser" type="button" class="reaction" aria-label="添加表情回应" :aria-expanded="choosing" :disabled="pending" @click="choosing = !choosing">☺ +</button>
      <button v-if="reactions.length" type="button" class="reaction" @click="showResponders">查看回应者</button>
    </div>
    <div v-if="choosing" class="post-reactions__picker">
      <p>每条帖子可选择一个回应，再点一次可撤销。</p>
      <button v-for="emoji in unicode" :key="emoji" type="button" class="reaction" :aria-label="`回应 ${emoji}`" @click="toggle(emoji)">{{ emoji }}</button>
      <EmojiPicker @select="choose" @close="choosing = false" />
    </div>
    <p v-if="error" role="alert">{{ error }}</p>
    <t-dialog v-model:visible="details" header="表情回应" :footer="false" width="440px">
      <p v-if="loadingDetails" role="status">加载回应者…</p>
      <p v-else-if="error" role="alert">{{ error }}</p>
      <p v-else-if="!responders.length">还没有回应</p>
      <section v-for="reaction in responders" :key="reaction.name">
        <h3>{{ reaction.is_sensitive ? '敏感表情' : reaction.name }} · {{ reaction.count }}</h3>
        <ul><li v-for="account in reaction.accounts || []" :key="account.id"><EmojiText :text="account.display_name || account.username" :emojis="account.emojis" /> <span>@{{ account.acct }}</span></li></ul>
        <p v-if="reaction.count > (reaction.accounts?.length || 0)">仅显示前 {{ reaction.accounts?.length || 0 }} 位回应者</p>
      </section>
    </t-dialog>
  </div>
</template>
<style scoped>
.post-reactions { margin-top: var(--space-2); font-size: var(--text-footnote); }
.post-reactions__row { display: flex; flex-wrap: wrap; gap: var(--space-1); }
.reaction { display: inline-flex; align-items: center; gap: var(--space-1); min-height: 2rem; padding: var(--space-1) var(--space-2); border-radius: var(--radius-full); background: var(--color-fill); }
.reaction img { width: 1.3em; height: 1.3em; object-fit: contain; }
.reaction--mine { background: var(--color-accent-soft); color: var(--color-accent-text); }
.post-reactions__picker { padding-top: var(--space-2); }
.post-reactions [role=alert] { color: var(--color-danger); }
</style>
