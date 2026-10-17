<script setup lang="ts">
import { displayAccount } from '@/shared/utils/accountDisplay'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { computed, ref, onBeforeUnmount, onMounted, watch } from 'vue'
import { userApi } from '@/domains/user/infrastructure/userApi'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import type { MastodonAccount } from '@/shared/types/mastodon'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import type { ChatMemberInput } from '../../domain/chat'

import { t } from '@/shared/i18n'
const props = defineProps<{ pending?: boolean; error?: string; mode?: 'create' | 'invite'; initialRecipient?: string }>()
const emit = defineEmits<{ submit: [members: ChatMemberInput[]]; cancel: [] }>()
const auth = useAuthStore()
const recipientInput = ref<HTMLInputElement>()
onMounted(() => recipientInput.value?.focus())
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
      candidate = { key: `matrix:${value}`, label: displayAccount(value), input: { matrix_id: value } }
    } else {
      const account = await userApi.lookupAccount(value)
      if (version !== generation) return
      if (account.id === auth.currentUser?.id) throw new Error(t('message.ConversationMembersForm.s16'))
      candidate = { key: `account:${account.id}`, label: `@${displayAccount(account.acct)}`, input: { account_id: account.id }, account }
    }
    if (version !== generation) return
    if (!selected.value.some(member => member.key === candidate.key)) selected.value.push(candidate)
    query.value = ''
  } catch (error) {
    if (version === generation) resolveError.value = error instanceof Error ? error.message : t('message.ConversationMembersForm.s17')
  } finally {
    if (version === generation) resolving.value = false
  }
}
function submit() { if (ready.value) emit('submit', selected.value.map(member => member.input)) }
</script>

<template>
  <form class="members-form" @submit.prevent="submit">
    <h2>{{ mode === 'invite' ? $t('message.ConversationMembersForm.s1') : $t('message.ConversationMembersForm.s2') }}</h2>
    <label class="members-form__label" for="conversation-recipient">{{ $t('message.ConversationMembersForm.s3') }}</label>
    <div class="members-form__search">
      <input ref="recipientInput" id="conversation-recipient" v-model="query" autocomplete="off" autocapitalize="none" spellcheck="false" :placeholder="$t('message.ConversationMembersForm.s4')" :disabled="pending || resolving" @keydown.enter.prevent="add" />
      <button class="btn btn--secondary btn--sm" type="button" :disabled="!query.trim() || pending || resolving" @click="add">{{ resolving ? $t('message.ConversationMembersForm.s5') : $t('message.ConversationMembersForm.s6') }}</button>
    </div>
    <p class="members-form__help">{{ $t('message.ConversationMembersForm.s7') }}</p>
    <p v-if="resolveError" class="members-form__error" role="alert">{{ resolveError }}</p>
    <ul v-if="selected.length" class="members-form__selected" :aria-label="$t('message.ConversationMembersForm.s8')">
      <li v-for="member in selected" :key="member.key">
        <UserAvatar v-if="member.account" :account="member.account" :size="32" />
        <span class="members-form__member-label">{{ member.label }}</span>
        <button type="button" class="btn btn--ghost btn--sm" :disabled="pending" :aria-label="$t('message.ConversationMembersForm.s9', [member.label])" @click="selected = selected.filter(item => item.key !== member.key)">{{ $t('message.ConversationMembersForm.s10') }}</button>
      </li>
    </ul>
    <label class="members-form__consent">
      <input v-model="consent" type="checkbox" :disabled="pending" />
      <span>{{ $t('message.ConversationMembersForm.s11') }}</span>
    </label>
    <p v-if="error" class="members-form__error" role="alert">{{ error }}</p>
    <div class="members-form__actions">
      <button class="btn btn--secondary btn--sm" type="button" :disabled="pending" @click="emit('cancel')">{{ $t('message.ConversationMembersForm.s12') }}</button>
      <button class="btn btn--primary btn--sm" type="submit" :disabled="!ready">{{ pending ? $t('message.ConversationMembersForm.s13') : mode === 'invite' ? $t('message.ConversationMembersForm.s14') : $t('message.ConversationMembersForm.s15') }}</button>
    </div>
  </form>
</template>

<style scoped>
.members-form { margin: var(--space-2) var(--gutter) var(--space-5); padding: var(--space-5); max-width: 680px; display: grid; gap: var(--space-3); min-width: 0; border: 1px solid var(--color-separator); border-radius: var(--radius-lg); background: var(--color-bg-grouped); }
.members-form h2 { margin: 0; font-size: var(--text-title3, 1.2rem); }
.members-form__label { font-weight: var(--weight-semibold); }
.members-form__search { display: flex; gap: var(--space-2); min-width: 0; }
.members-form__search input { min-width: 0; flex: 1; padding: var(--space-2); border: 1px solid var(--color-separator); min-height: var(--hit-target); font: inherit; border-radius: var(--radius-sm); background: var(--color-bg); color: var(--color-label); }
.members-form__search input:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 2px; }
.members-form__help { margin: 0; font-size: var(--text-footnote); color: var(--color-label-secondary); }
.members-form__selected { list-style: none; padding: 0; margin: 0; }
.members-form__selected li { display: flex; gap: var(--space-2); align-items: center; padding-block: var(--space-2); }
.members-form__selected :deep(.avatar-frame) { flex: 0 0 32px; width: 32px; height: 32px; }
.members-form__member-label { flex: 1; min-width: 0; overflow-wrap: anywhere; }
.members-form__consent { display: flex; align-items: flex-start; gap: var(--space-2); font-size: var(--text-footnote); line-height: 1.6; }
.members-form__consent input { flex: 0 0 auto; margin-top: .3em; }
.members-form__actions { padding-top: var(--space-3); border-top: 1px solid var(--color-separator); display: flex; justify-content: flex-end; gap: var(--space-2); }
.members-form__error { margin: 0; color: var(--color-danger); overflow-wrap: anywhere; }
@media (max-width: 43.74rem) { .members-form { padding: var(--space-4); margin-inline: var(--space-3); } }
</style>
