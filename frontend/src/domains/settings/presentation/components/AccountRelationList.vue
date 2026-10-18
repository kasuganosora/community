<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { MessagePlugin } from '@/shared/ui/feedback'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import EmojiText from '@/shared/components/common/EmojiText.vue'
import { displayAccount } from '@/shared/utils/accountDisplay'
import type { MastodonAccount } from '@/shared/types/mastodon'
import { moderationApi, type AccountListKind } from '../../infrastructure/moderationApi'
import { useRelationList } from '../composables/useRelationList'
import { apiErrorMessage } from '../composables/apiErrorMessage'
import RelationOrderToggle from './RelationOrderToggle.vue'
import { t } from '@/shared/i18n'

const props = defineProps<{ kind: AccountListKind }>()

const list = useRelationList<MastodonAccount>(
  (order, next) => moderationApi.listAccounts(props.kind, order, next),
  (account) => account.id,
  () => t('settings.Blocks.loadFailed'),
  { immediate: true },
)
const acting = ref('')

const text = computed(() => ({
  hidden_reblogs: { empty: t('settings.Blocks.hiddenReblogsEmpty'), action: t('settings.Blocks.showReblogs'), done: t('settings.Blocks.showReblogsDone') },
  mutes: { empty: t('settings.Blocks.mutesEmpty'), action: t('settings.Blocks.unmute'), done: t('settings.Blocks.unmuteDone') },
  blocks: { empty: t('settings.Blocks.blocksEmpty'), action: t('settings.Blocks.unblock'), done: t('settings.Blocks.unblockDone') },
}[props.kind]))

async function undo(account: MastodonAccount) {
  if (acting.value) return
  const current = list.scope()
  acting.value = account.id
  try {
    await moderationApi.undo(props.kind, account.id)
    if (!current()) return
    list.removeItem(account.id)
    MessagePlugin.success(text.value.done)
  } catch (e) {
    if (current()) MessagePlugin.error(apiErrorMessage(e, t('settings.Blocks.actionFailed')))
  } finally {
    if (current()) acting.value = ''
  }
}

// Mounted the first time its section is opened.
onMounted(() => { void list.ensureLoaded() })
</script>

<template>
  <div class="relation-list" :data-testid="`relation-list-${kind}`">
    <div class="relation-list__toolbar">
      <RelationOrderToggle :model-value="list.order.value" :disabled="list.loading.value" @update:model-value="list.setOrder" />
    </div>
    <div v-if="list.error.value && !list.items.value.length" class="settings-error" role="alert">
      {{ list.error.value }}
      <button type="button" class="btn btn--ghost btn--sm" @click="list.reload">{{ $t('settings.FilterSettings.s2') }}</button>
    </div>
    <p v-else-if="list.loaded.value && !list.items.value.length" class="settings-field__hint relation-list__empty">{{ text.empty }}</p>
    <ul v-if="list.items.value.length" class="relation-list__items">
      <li v-for="account in list.items.value" :key="account.id" class="relation-list__item" data-testid="relation-account">
        <router-link :to="`/@${account.acct}`" class="relation-list__account">
          <UserAvatar :account="account" :size="40" />
          <span class="relation-list__names">
            <span class="relation-list__name"><EmojiText :text="account.display_name || account.username" :emojis="account.emojis" /></span>
            <span class="relation-list__acct">@{{ displayAccount(account.acct) }}</span>
          </span>
        </router-link>
        <button type="button" class="btn btn--ghost btn--sm relation-list__action" :disabled="!!acting" :aria-busy="acting === account.id" @click="undo(account)">{{ text.action }}</button>
      </li>
    </ul>
    <p v-if="list.loading.value" class="settings-field__hint" role="status">{{ $t('settings.FilterSettings.s1') }}</p>
    <p v-else-if="list.error.value && list.items.value.length" class="settings-error" role="alert">{{ list.error.value }}</p>
    <button v-if="list.next.value && !list.loading.value" type="button" class="btn btn--ghost btn--sm relation-list__more" @click="list.loadMore">{{ $t('settings.Blocks.loadMore') }}</button>
  </div>
</template>

<style scoped>
.relation-list { display: flex; flex-direction: column; gap: .5rem; padding-top: .75rem; min-width: 0; }
.relation-list__toolbar { display: flex; justify-content: flex-end; }
.relation-list__empty { padding: .5rem 0; }
.relation-list__items { display: flex; flex-direction: column; padding: 0; list-style: none; }
.relation-list__item { display: flex; align-items: center; gap: .75rem; padding: .625rem 0; border-bottom: 1px solid var(--color-separator); min-width: 0; }
.relation-list__item:last-child { border-bottom: 0; }
.relation-list__account { display: flex; flex: 1; align-items: center; gap: .75rem; min-width: 0; color: inherit; }
.relation-list__names { display: flex; flex-direction: column; min-width: 0; }
.relation-list__name { font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.relation-list__acct { color: var(--color-label-secondary); font-size: .8125rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.relation-list__action { flex: none; min-height: 40px; }
.relation-list__more { align-self: center; }
</style>
