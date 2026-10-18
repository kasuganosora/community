<script setup lang="ts">
import RichText from '@/shared/components/common/RichText.vue'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import { onMounted, onBeforeUnmount, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import type { MastodonAccount } from '@/shared/types/mastodon'
import { moderationAdminApi as api, type AdminAccount, type AccountAction } from '../../infrastructure/moderationAdminApi'
import { t } from '@/shared/i18n'
function originFromQuery(value: unknown): string | undefined {
  const raw = Array.isArray(value) ? value[0] : value
  if (typeof raw !== 'string') return undefined
  if (raw === 'local' || raw === 'remote' || raw === '') return raw
  return undefined
}
const route = useRoute()
const queryOrigin = originFromQuery(route.query.origin)
const filters = reactive({ origin: queryOrigin !== undefined ? queryOrigin : 'local', status: '', username: '', display_name: '' })
const items = ref<AdminAccount[]>([])
const selected = ref<AdminAccount | null>(null)
const offset = ref(0)
const total = ref<number | null>(null)
const limit = 40
const loading = ref(false)
const busy = ref(false)
const detailLoading = ref(false)
const error = ref('')
let listRequest = 0
let detailRequest = 0
const names: Record<AccountAction, string> = { disable: t('admin.AccountsAdminPage.s40'), enable: t('admin.AccountsAdminPage.s39'), suspend: t('admin.AccountsAdminPage.s42'), unsuspend: t('admin.AccountsAdminPage.s41'), approve: t('admin.AccountsAdminPage.s37'), reject: t('admin.AccountsAdminPage.s38') }
function state(a: AdminAccount) {
  if (a.suspended) return t('admin.AccountsAdminPage.s14')
  if (a.user_status === 1) return t('admin.AccountsAdminPage.s12')
  if (a.user_status === 2 || a.disabled) return t('admin.AccountsAdminPage.s13')
  if (a.user_status === 3) return t('admin.AccountsAdminPage.s43')
  return t('admin.AccountsAdminPage.s11')
}
function message(e: unknown) { return e instanceof Error ? e.message : t('admin.AccountsAdminPage.s44') }
/** Prefer nested Mastodon account (proxied avatar + decorations); fall back to admin fields. */
function avatarAccount(a: AdminAccount): MastodonAccount {
  if (a.account) return a.account
  const acct = a.domain ? `${a.username}@${a.domain}` : a.username
  return {
    id: a.id,
    username: a.username,
    acct,
    display_name: a.display_name || a.username,
    locked: a.locked,
    bot: false,
    discoverable: false,
    indexable: false,
    group: false,
    created_at: a.created_at || '',
    note: '',
    url: '',
    uri: '',
    avatar: a.avatar || '',
    avatar_default: a.avatar_default,
    avatar_static: a.avatar_static || a.avatar || '',
    avatar_decorations: a.avatar_decorations,
    avatar_decoration_url: a.avatar_decoration_url,
    avatar_decoration_id: a.avatar_decoration_id,
    header: '',
    header_static: '',
    followers_count: 0,
    following_count: 0,
    statuses_count: 0,
    last_status_at: null,
    hide_collections: false,
    emojis: [],
    fields: [],
  }
}
async function load(reset = false) {
  if (reset) { offset.value = 0; selected.value = null; detailLoading.value = false; detailRequest++ }
  const version = ++listRequest
  loading.value = true
  error.value = ''
  try {
    const result = await api.accounts({ ...filters, username: filters.username.trim(), display_name: filters.display_name.trim(), offset: offset.value, limit })
    if (version !== listRequest) return
    items.value = result.items
    total.value = result.total
  } catch (e) { if (version === listRequest) { error.value = message(e); items.value = [] } }
  finally { if (version === listRequest) loading.value = false }
}
// Two-factor state of the selected local account (null: unknown / remote).
const twoFactor = ref<{ enabled: boolean; enabled_at: string | null } | null>(null)
async function loadTwoFactor(account: AdminAccount, version: number) {
  twoFactor.value = null
  if (account.domain) return
  try { const value = await api.accountTwoFactor(account.id); if (version === detailRequest) twoFactor.value = value }
  catch { /* not a local login account */ }
}
async function resetTwoFactor() {
  const account = selected.value
  if (!account || busy.value || !window.confirm(t('admin.AccountsAdminPage.twoFactorResetConfirm', [account.username]))) return
  busy.value = true
  error.value = ''
  try {
    await api.resetAccountTwoFactor(account.id)
    twoFactor.value = { enabled: false, enabled_at: null }
  } catch (e) { error.value = message(e) }
  finally { busy.value = false }
}
async function detail(id: string) {
  const version = ++detailRequest
  selected.value = null
  twoFactor.value = null
  detailLoading.value = true
  error.value = ''
  try {
    const value = await api.account(id)
    if (version === detailRequest) { selected.value = value; void loadTwoFactor(value, version) }
  }
  catch (e) { if (version === detailRequest) error.value = message(e) }
  finally { if (version === detailRequest) detailLoading.value = false }
}
async function act(action: AccountAction) {
  const account = selected.value
  if (!account || busy.value || !window.confirm(t('admin.AccountsAdminPage.s45', [account.username, account.domain ? `@${account.domain}` : '', names[action]]))) return
  busy.value = true
  error.value = ''
  try {
    await api.accountAction(account.id, action)
    await detail(account.id)
    await load()
  } catch (e) { error.value = message(e) }
  finally { busy.value = false }
}
async function page(delta: number) { offset.value = Math.max(0, offset.value + delta * limit); await load() }
onMounted(() => load())
onBeforeUnmount(() => { listRequest++; detailRequest++ })
</script>
<template>
  <section class="admin-page">
    <header>
      <h1>{{ $t('admin.AccountsAdminPage.s1') }}</h1>
      <p>{{ $t('admin.AccountsAdminPage.s2') }}</p>
    </header>
    <form class="admin-toolbar" :aria-label="$t('admin.AccountsAdminPage.s3')" @submit.prevent="!busy && load(true)">
      <label class="admin-field">{{ $t('admin.AccountsAdminPage.s4') }}<input v-model="filters.username" /></label>
      <label class="admin-field">{{ $t('admin.AccountsAdminPage.s5') }}<input v-model="filters.display_name" /></label>
      <label class="admin-field">{{ $t('admin.AccountsAdminPage.s6') }}<select v-model="filters.origin"><option value="">{{ $t('admin.AccountsAdminPage.s7') }}</option><option value="local">{{ $t('admin.AccountsAdminPage.s8') }}</option><option value="remote">{{ $t('admin.AccountsAdminPage.s9') }}</option></select></label>
      <label class="admin-field">{{ $t('admin.AccountsAdminPage.s10') }}<select v-model="filters.status"><option value="">{{ $t('admin.AccountsAdminPage.s7') }}</option><option value="active">{{ $t('admin.AccountsAdminPage.s11') }}</option><option value="pending">{{ $t('admin.AccountsAdminPage.s12') }}</option><option value="disabled">{{ $t('admin.AccountsAdminPage.s13') }}</option><option value="suspended">{{ $t('admin.AccountsAdminPage.s14') }}</option></select></label>
      <button class="admin-button admin-button--primary" :disabled="loading || busy">{{ $t('admin.AccountsAdminPage.s15') }}</button>
    </form>
    <p v-if="error" class="admin-error" role="alert">{{ error }}</p>
    <p v-if="loading" class="admin-empty" role="status">{{ $t('admin.AccountsAdminPage.s16') }}</p>
    <div class="admin-table-wrap admin-card accounts-table-wrap" role="region" :aria-label="$t('admin.AccountsAdminPage.s17')" tabindex="0">
      <table class="admin-table accounts-table">
        <thead><tr><th>{{ $t('admin.AccountsAdminPage.s18') }}</th><th>{{ $t('admin.AccountsAdminPage.s6') }}</th><th>{{ $t('admin.AccountsAdminPage.s10') }}</th><th>{{ $t('admin.AccountsAdminPage.s19') }}</th><th>{{ $t('admin.AccountsAdminPage.s20') }}</th></tr></thead>
        <tbody>
          <tr v-for="a in items" :key="a.id">
            <td class="accounts-table__account">
              <div class="accounts-table__identity">
                <UserAvatar class="accounts-table__avatar" :account="avatarAccount(a)" :size="38" />
                <div class="accounts-table__names">
                  <span class="accounts-table__display">{{ a.display_name || a.username }}</span>
                  <small>@{{ a.username }}{{ a.domain ? `@${a.domain}` : '' }}</small>
                </div>
              </div>
            </td>
            <td>{{ a.domain || $t('admin.AccountsAdminPage.s8') }}</td>
            <td><span class="admin-badge">{{ state(a) }}</span></td>
            <td>{{ a.role === 'admin' ? $t('admin.AccountsAdminPage.s21') : a.role === 'user' ? $t('admin.AccountsAdminPage.s22') : '—' }}</td>
            <td><button class="admin-button" :disabled="loading || busy" @click="detail(a.id)">{{ $t('admin.AccountsAdminPage.s23') }}</button></td>
          </tr>
        </tbody>
      </table>
      <p v-if="!loading && !error && !items.length" class="admin-empty">{{ $t('admin.AccountsAdminPage.s24') }}</p>
    </div>
    <nav class="admin-pagination" :aria-label="$t('admin.AccountsAdminPage.s25')">
      <button class="admin-button" :disabled="loading || busy || offset === 0" @click="page(-1)">{{ $t('admin.AccountsAdminPage.s26') }}</button>
      <span>{{ $t('admin.AccountsAdminPage.s28', [Math.floor(offset / limit) + 1, total !== null ? $t('admin.AccountsAdminPage.s27', [total]) : '']) }}</span>
      <button class="admin-button" :disabled="loading || busy || (total !== null ? offset + limit >= total : items.length < limit)" @click="page(1)">{{ $t('admin.AccountsAdminPage.s29') }}</button>
    </nav>
    <p v-if="detailLoading" class="admin-empty" role="status">{{ $t('admin.AccountsAdminPage.s30') }}</p>
    <section v-if="selected" class="admin-card" :aria-label="$t('admin.AccountsAdminPage.s31')">
      <h2>@{{ selected.username }}{{ selected.domain ? `@${selected.domain}` : '' }}</h2>
      <dl><dt>{{ $t('admin.AccountsAdminPage.s32') }}</dt><dd>{{ selected.id }}</dd><dt>{{ $t('admin.AccountsAdminPage.s5') }}</dt><dd>{{ selected.display_name || selected.username }}</dd><dt>{{ $t('admin.AccountsAdminPage.s10') }}</dt><dd>{{ state(selected) }}</dd><dt>{{ $t('admin.AccountsAdminPage.s19') }}</dt><dd>{{ selected.role === 'admin' ? $t('admin.AccountsAdminPage.s21') : selected.role === 'user' ? $t('admin.AccountsAdminPage.s22') : '—' }}</dd><dt>{{ $t('admin.AccountsAdminPage.s33') }}</dt><dd>{{ selected.created_at ? new Date(selected.created_at).toLocaleString() : '—' }}</dd><dt>{{ $t('admin.AccountsAdminPage.s34') }}</dt><dd>{{ selected.locked ? $t('admin.AccountsAdminPage.s35') : $t('admin.AccountsAdminPage.s36') }}</dd><template v-if="twoFactor"><dt>{{ $t('admin.AccountsAdminPage.twoFactor') }}</dt><dd>{{ twoFactor.enabled ? $t('admin.AccountsAdminPage.twoFactorOn') : $t('admin.AccountsAdminPage.twoFactorOff') }}</dd></template></dl>
      <RichText v-if="selected.account?.note" :html="selected.account.note" :emojis="selected.account.emojis" />
      <div class="admin-toolbar">
        <template v-if="!selected.domain && selected.user_status !== undefined">
          <template v-if="selected.user_status === 1"><button class="admin-button admin-button--primary" :disabled="busy" @click="act('approve')">{{ $t('admin.AccountsAdminPage.s37') }}</button><button class="admin-button admin-button--danger" :disabled="busy" @click="act('reject')">{{ $t('admin.AccountsAdminPage.s38') }}</button></template>
          <button v-else-if="selected.user_status === 2 || selected.disabled" class="admin-button" :disabled="busy" @click="act('enable')">{{ $t('admin.AccountsAdminPage.s39') }}</button>
          <button v-else class="admin-button admin-button--danger" :disabled="busy" @click="act('disable')">{{ $t('admin.AccountsAdminPage.s40') }}</button>
        </template>
        <button v-if="twoFactor?.enabled" class="admin-button admin-button--danger" :disabled="busy" @click="resetTwoFactor">{{ $t('admin.AccountsAdminPage.twoFactorReset') }}</button>
        <button v-if="selected.suspended" class="admin-button" :disabled="busy" @click="act('unsuspend')">{{ $t('admin.AccountsAdminPage.s41') }}</button>
        <button v-else class="admin-button admin-button--danger" :disabled="busy" @click="act('suspend')">{{ $t('admin.AccountsAdminPage.s42') }}</button>
      </div>
    </section>
  </section>
</template>
<style scoped>
/* Decorations are 200% of the avatar and centred on it; pad so the table/scroll
   region does not clip the overhang. */
.accounts-table-wrap {
  overflow-x: auto;
  overflow-y: visible;
}
.accounts-table {
  border-collapse: separate;
  border-spacing: 0;
}
.accounts-table__account {
  padding: 22px 20px 22px 24px;
  min-width: 220px;
  overflow: visible;
}
.accounts-table__identity {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}
.accounts-table__avatar {
  flex-shrink: 0;
}
.accounts-table__names {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}
.accounts-table__display {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.accounts-table__names small {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
@media (max-width: 760px) {
  .accounts-table__account {
    padding: 20px 14px 20px 18px;
    min-width: 200px;
  }
  .accounts-table__identity {
    gap: 10px;
  }
}
</style>
