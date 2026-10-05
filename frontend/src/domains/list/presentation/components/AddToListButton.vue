<script setup lang="ts">
import { ref } from 'vue'
import type { MastodonList } from '@/shared/types/mastodon'
import { listApi } from '../../infrastructure/listApi'

const props = defineProps<{ accountId: string }>()

const open = ref(false)
const loading = ref(false)
const error = ref<string | null>(null)
const lists = ref<MastodonList[]>([])
const memberOf = ref<Set<string>>(new Set())
const busyId = ref<string | null>(null)

async function show() {
  open.value = true
  loading.value = true
  error.value = null
  try {
    const [all, mine] = await Promise.all([
      listApi.getLists(),
      listApi.listsContaining(props.accountId),
    ])
    lists.value = all
    memberOf.value = new Set(mine.map(list => list.id))
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载列表失败'
  } finally {
    loading.value = false
  }
}

async function toggle(list: MastodonList) {
  busyId.value = list.id
  error.value = null
  const joined = memberOf.value.has(list.id)
  try {
    if (joined) await listApi.removeAccounts(list.id, [props.accountId])
    else await listApi.addAccounts(list.id, [props.accountId])
    const next = new Set(memberOf.value)
    if (joined) next.delete(list.id)
    else next.add(list.id)
    memberOf.value = next
  } catch (e) {
    error.value = e instanceof Error ? e.message : '更新列表失败'
  } finally {
    busyId.value = null
  }
}
</script>

<template>
  <button type="button" class="btn btn--secondary btn--sm" @click="show">加入列表</button>
  <t-dialog v-model:visible="open" header="加入列表" :footer="false" width="420px">
    <p v-if="error" class="add-list__error" role="alert">{{ error }}</p>
    <div v-if="loading" class="page-loading"><t-loading /></div>
    <p v-else-if="lists.length === 0" class="add-list__empty">
      还没有列表。
      <router-link to="/lists" @click="open = false">去创建一个</router-link>
    </p>
    <ul v-else class="add-list__items">
      <li v-for="list in lists" :key="list.id" class="add-list__item">
        <span>{{ list.title }}</span>
        <button type="button" class="btn btn--secondary btn--sm" :disabled="busyId === list.id" @click="toggle(list)">
          {{ memberOf.has(list.id) ? '移出' : '加入' }}
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
