<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import type { MastodonList } from '@/shared/types/mastodon'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { listApi } from '../../infrastructure/listApi'

import { t } from '@/shared/i18n'
const props = defineProps<{ accountId: string }>()

const open = ref(false)
const loading = ref(false)
const error = ref<string | null>(null)
const lists = ref<MastodonList[]>([])
const memberOf = ref<Set<string>>(new Set())
const busyId = ref<string | null>(null)
let version = 0
function reset() {
  version++
  open.value = false; loading.value = false; error.value = null
  lists.value = []; memberOf.value = new Set(); busyId.value = null
}
watch([() => props.accountId, sessionEpoch], reset, { flush: 'sync' })
onBeforeUnmount(reset)

async function show() {
  if (loading.value || busyId.value) return
  const request = ++version, accountId = props.accountId
  open.value = true
  loading.value = true
  error.value = null
  try {
    const [all, mine] = await Promise.all([
      listApi.getLists(),
      listApi.listsContaining(accountId),
    ])
    if (request !== version) return
    lists.value = all
    memberOf.value = new Set(mine.map(list => list.id))
  } catch (e) {
    if (request === version) error.value = e instanceof Error ? e.message : t('list.AddToListButton.s9')
  } finally {
    if (request === version) loading.value = false
  }
}

async function toggle(list: MastodonList) {
  if (busyId.value || loading.value) return
  const request = version, accountId = props.accountId
  busyId.value = list.id
  error.value = null
  const joined = memberOf.value.has(list.id)
  try {
    if (joined) await listApi.removeAccounts(list.id, [accountId])
    else await listApi.addAccounts(list.id, [accountId])
    if (request !== version) return
    const next = new Set(memberOf.value)
    if (joined) next.delete(list.id)
    else next.add(list.id)
    memberOf.value = next
  } catch (e) {
    if (request === version) error.value = e instanceof Error ? e.message : t('list.AddToListButton.s10')
  } finally {
    if (request === version) busyId.value = null
  }
}
</script>

<template>
  <button type="button" class="btn btn--secondary btn--sm" @click="show">{{ $t('list.AddToListButton.s1') }}</button>
  <t-dialog v-model:visible="open" :header="$t('list.AddToListButton.s1')" :footer="false" width="420px">
    <p v-if="error" class="add-list__error" role="alert">{{ error }} <button v-if="!lists.length" type="button" class="btn btn--secondary btn--sm" @click="show">{{ $t('list.AddToListButton.s2') }}</button></p>
    <div v-if="loading" class="page-loading" role="status" :aria-label="$t('list.AddToListButton.s3')"><t-loading /></div>
    <p v-else-if="!error && lists.length === 0" class="add-list__empty">
      {{ $t('list.AddToListButton.s4') }}
      <router-link to="/lists" @click="open = false">{{ $t('list.AddToListButton.s5') }}</router-link>
    </p>
    <ul v-else-if="lists.length" class="add-list__items">
      <li v-for="list in lists" :key="list.id" class="add-list__item">
        <span>{{ list.title }}</span>
        <button type="button" class="btn btn--secondary btn--sm" :disabled="!!busyId" @click="toggle(list)">
          {{ busyId === list.id ? $t('list.AddToListButton.s6') : memberOf.has(list.id) ? $t('list.AddToListButton.s7') : $t('list.AddToListButton.s8') }}
        </button>
      </li>
    </ul>
  </t-dialog>
</template>

<style scoped>
.add-list__error {
  color: var(--color-danger, var(--accent-red, #e0245e));
  font-size: var(--text-footnote);
}

.add-list__empty {
  color: var(--color-label-secondary);
  font-size: var(--text-subhead);
}

.add-list__items {
  margin: 0;
  padding: 0;
  list-style: none;
}

.add-list__item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  min-height: 2.75rem;
}
</style>
