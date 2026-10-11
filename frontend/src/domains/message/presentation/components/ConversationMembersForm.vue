<script setup lang="ts">
import { sessionEpoch } from '@/shared/api/sessionScope'
import { computed, ref, onBeforeUnmount, watch } from 'vue'
import { userApi } from '@/domains/user/infrastructure/userApi'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import type { MastodonAccount } from '@/shared/types/mastodon'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import type { ChatMemberInput } from '../../domain/chat'

const props = defineProps<{ pending?: boolean; error?: string; mode?: 'create' | 'invite'; initialRecipient?: string }>()
const emit = defineEmits<{ submit: [members: ChatMemberInput[]]; cancel: [] }>()
const auth = useAuthStore()
const query = ref(props.initialRecipient || '')
const resolving = ref(false)
const resolveError = ref('')
const consent = ref(false)
const selected = ref<{ key: string; label: string; input: ChatMemberInput; account?: MastodonAccount }[]>([])
let generation = 0
const ready = computed(() => consent.value && selected.value.length > 0 && !props.pending && !resolving.value)
function reset() { generation++; query.value = ''; selected.value = []; consent.value = false; resolveError.value = ''; resolving.value = false }
watch(() => sessionEpoch.value, reset, { flush: 'sync' })
onBeforeUnmount(() => { generation++ })
async function add() {
  const value = query.value.trim()
  if (!value || resolving.value || props.pending) return
  const version = ++generation
  resolving.value = true
  resolveError.value = ''
  try {
    let candidate: (typeof selected.value)[number]
    if (/^@[^@:\s]+:[^\s]+$/.test(value)) {
      candidate = { key: `matrix:${value}`, label: value, input: { matrix_id: value } }
    } else {
      const account = await userApi.lookupAccount(value)
      if (version !== generation) return
      if (account.id === auth.currentUser?.id) throw new Error('你已经是会话成员，无需重复添加。')
      candidate = { key: `account:${account.id}`, label: `@${account.acct}`, input: { account_id: account.id }, account }
    }
    if (version !== generation) return
    if (!selected.value.some(member => member.key === candidate.key)) selected.value.push(candidate)
    query.value = ''
  } catch (error) {
    if (version === generation) resolveError.value = error instanceof Error ? error.message : '无法找到这个账号，请检查完整地址。'
  } finally {
    if (version === generation) resolving.value = false
  }
}
function submit() { if (ready.value) emit('submit', selected.value.map(member => member.input)) }
</script>

<template>
  <form class="members-form" @submit.prevent="submit">
    <h2>{{ mode === 'invite' ? '添加会话成员' : '新建会话' }}</h2>
    <label class="members-form__label" for="conversation-recipient">完整账号地址</label>
    <div class="members-form__search">
      <input id="conversation-recipient" v-model="query" autocomplete="off" placeholder="user@站点 或 @user:Matrix服务器" :disabled="pending || resolving" @keydown.enter.prevent="add" />
      <button class="btn btn--secondary btn--sm" type="button" :disabled="!query.trim() || pending || resolving" @click="add">{{ resolving ? '查找中…' : '添加' }}</button>
    </div>
    <p class="members-form__help">可以添加多个 Community、Mastodon、Misskey 或 Matrix 账号。请核对完整地址。</p>
    <p v-if="resolveError" class="members-form__error" role="alert">{{ resolveError }}</p>
    <ul v-if="selected.length" class="members-form__selected" aria-label="待添加的成员">
      <li v-for="member in selected" :key="member.key">
        <UserAvatar v-if="member.account" :account="member.account" :size="32" />
        <span class="members-form__member-label">{{ member.label }}</span>
        <button type="button" class="btn btn--ghost btn--sm" :disabled="pending" :aria-label="`移除 ${member.label}`" @click="selected = selected.filter(item => item.key !== member.key)">移除</button>
      </li>
    </ul>
    <label class="members-form__consent">
      <input v-model="consent" type="checkbox" :disabled="pending" />
      <span>我知道此会话不使用端到端加密。参与站点的服务器可读取消息；新加入的成员不能读取加入前的历史。</span>
    </label>
    <p v-if="error" class="members-form__error" role="alert">{{ error }}</p>
    <div class="members-form__actions">
      <button class="btn btn--secondary btn--sm" type="button" :disabled="pending" @click="emit('cancel')">取消</button>
      <button class="btn btn--primary btn--sm" type="submit" :disabled="!ready">{{ pending ? '处理中…' : mode === 'invite' ? '确认添加' : '创建会话' }}</button>
    </div>
  </form>
</template>

<style scoped>
.members-form { padding: var(--space-4); display: grid; gap: var(--space-3); min-width: 0; }
.members-form h2 { margin: 0; font-size: var(--text-title3, 1.2rem); }
.members-form__label { font-weight: var(--weight-semibold); }
.members-form__search { display: flex; gap: var(--space-2); min-width: 0; }
.members-form__search input { min-width: 0; flex: 1; padding: var(--space-2); border: 1px solid var(--color-separator); border-radius: var(--radius-sm); background: var(--color-fill); color: var(--color-label); }
.members-form__help { margin: 0; font-size: var(--text-footnote); color: var(--color-label-secondary); }
.members-form__selected { list-style: none; padding: 0; margin: 0; }
.members-form__selected li { display: flex; gap: var(--space-2); align-items: center; padding-block: var(--space-2); }
.members-form__selected :deep(.avatar-frame) { flex: 0 0 32px; width: 32px; height: 32px; }
.members-form__member-label { flex: 1; min-width: 0; overflow-wrap: anywhere; }
.members-form__consent { display: flex; align-items: flex-start; gap: var(--space-2); font-size: var(--text-footnote); line-height: 1.6; }
.members-form__consent input { flex: 0 0 auto; margin-top: .3em; }
.members-form__actions { display: flex; justify-content: flex-end; gap: var(--space-2); }
.members-form__error { margin: 0; color: var(--color-danger); overflow-wrap: anywhere; }
</style>
