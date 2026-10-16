<script setup lang="ts">
import { AdminAlert, AdminBadge, AdminButton, AdminCheck, AdminInlineForm, AdminEmpty, AdminField, AdminPageHeader, AdminRow } from "../components/ui"
import { onMounted, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { accessAdminApi as api, permissionLabels, type AdminRole, type RoleDraft, type RoleMember } from '../../infrastructure/accessAdminApi'
import { t } from '@/shared/i18n'
const roles = ref<AdminRole[]>([]), permissions = ref<string[]>([]), members = ref<RoleMember[]>([])
const selected = ref<AdminRole | null>(null), editing = ref<string | null>(null)
const offset = ref(0), total = ref(0), memberOffset = ref(0), memberTotal = ref(0)
const error = ref(''), notice = ref(''), accountID = ref(''), loading = ref(false), busy = ref(false)
const memberLoading = ref(false), memberError = ref('')
const draft = reactive<RoleDraft>({ name: '', color: '', highlighted: false, permissions: [] })
let alive = true, listVersion = 0, memberVersion = 0
onBeforeUnmount(() => { alive = false; listVersion++; memberVersion++ })
watch(sessionEpoch, () => { listVersion++; memberVersion++; roles.value = []; members.value = []; permissions.value = []; selected.value = null; editing.value = null; loading.value = false; busy.value = false; memberLoading.value = false; error.value = ''; memberError.value = ''; notice.value = ''; accountID.value = ''; total.value = 0; memberTotal.value = 0; edit() }, { flush: 'sync' })
async function load() { const version = ++listVersion; loading.value = true; error.value = ''; try { const data = await api.roles(offset.value); if (!alive || version !== listVersion) return; roles.value = data.items; total.value = data.total; permissions.value = data.available_permissions } catch { if (alive && version === listVersion) error.value = t('admin.RolesAdminPage.s34') } finally { if (alive && version === listVersion) loading.value = false } }
function edit(role?: AdminRole) { editing.value = role?.id ?? null; Object.assign(draft, { name: role?.name ?? '', color: role?.color ?? '', highlighted: role?.highlighted ?? false, permissions: [...(role?.permissions ?? [])] }); notice.value = '' }
async function save() { if (busy.value || loading.value) return; const epoch = sessionEpoch.value; busy.value = true; error.value = ''; notice.value = ''; try { await api.saveRole(editing.value, { ...draft, permissions: [...draft.permissions] }); if (!alive || epoch !== sessionEpoch.value) return; edit(); notice.value = t('admin.RolesAdminPage.s35'); await load() } catch { if (alive && epoch === sessionEpoch.value) error.value = t('admin.RolesAdminPage.s36') } finally { if (alive && epoch === sessionEpoch.value) busy.value = false } }
async function remove(role: AdminRole) { if (busy.value) return; const epoch = sessionEpoch.value; if (!window.confirm(t('admin.RolesAdminPage.s37', [role.name]))) return; busy.value = true; error.value = ''; try { await api.deleteRole(role.id); if (!alive || epoch !== sessionEpoch.value) return; if (selected.value?.id === role.id) { selected.value = null; members.value = []; memberVersion++ } if (editing.value === role.id) edit(); await load() } catch { if (alive && epoch === sessionEpoch.value) error.value = t('admin.RolesAdminPage.s38') } finally { if (alive && epoch === sessionEpoch.value) busy.value = false } }
async function loadMembers() {
  const role = selected.value
  if (!role) return
  const version = ++memberVersion
  memberLoading.value = true; memberError.value = ''
  try {
    const data = await api.members(role.id, memberOffset.value)
    if (alive && version === memberVersion) { members.value = data.items; memberTotal.value = data.total }
  } catch {
    if (alive && version === memberVersion) memberError.value = t('admin.RolesAdminPage.s39')
  } finally {
    if (alive && version === memberVersion) memberLoading.value = false
  }
}
function select(role: AdminRole) { selected.value = role; members.value = []; memberTotal.value = 0; memberOffset.value = 0; accountID.value = ''; void loadMembers() }
async function changeMember(account: string, assign: boolean) { const role = selected.value; if (!role || busy.value || memberLoading.value || !/^\d+$/.test(account)) return; const epoch = sessionEpoch.value; if (!window.confirm(assign ? t('admin.RolesAdminPage.s40') : t('admin.RolesAdminPage.s41'))) return; busy.value = true; error.value = ''; try { await api.changeMember(role.id, account, assign); if (!alive || epoch !== sessionEpoch.value) return; accountID.value = ''; await loadMembers(); notice.value = t('admin.RolesAdminPage.s42') } catch { if (alive && epoch === sessionEpoch.value) error.value = t('admin.RolesAdminPage.s43') } finally { if (alive && epoch === sessionEpoch.value) busy.value = false } }
function permissionText(role: AdminRole) { return role.permissions.map(permission => permissionLabels[permission] || permission).join('、') || t('admin.RolesAdminPage.s44') }
function page(delta: number) { offset.value = Math.max(0, offset.value + delta * 40); void load() }
function memberPage(delta: number) { memberOffset.value = Math.max(0, memberOffset.value + delta * 40); void loadMembers() }
onMounted(load)
</script>

<template>
  <section class="admin-page">
    <AdminPageHeader :title="$t('admin.RolesAdminPage.s1')" :description="$t('admin.RolesAdminPage.s2')">
      <AdminButton size="sm" :disabled="loading || busy" @click="load">{{ $t('admin.RolesAdminPage.s3') }}</AdminButton>
    </AdminPageHeader>
    <AdminAlert v-if="error" tone="danger">{{ error }}</AdminAlert>
    <AdminAlert v-if="notice" tone="success">{{ notice }}</AdminAlert>
    <form class="admin-card" :aria-label="$t('admin.RolesAdminPage.s4')" @submit.prevent="save">
      <h2>{{ editing ? $t('admin.RolesAdminPage.s5') : $t('admin.RolesAdminPage.s6') }}</h2>
      <div class="admin-form admin-form--columns">
        <AdminField :label="$t('admin.RolesAdminPage.s7')"><template #default="{ id }"><input :id="id" v-model="draft.name" required maxlength="20"></template></AdminField>
        <AdminField :label="$t('admin.RolesAdminPage.s8')"><template #default="{ id }"><input :id="id" v-model="draft.color" placeholder="#2463eb" pattern="#[0-9a-fA-F]{6}"></template></AdminField>
        <AdminCheck v-model="draft.highlighted" :label="$t('admin.RolesAdminPage.s9')" />
        <fieldset class="admin-check-group"><legend>{{ $t('admin.RolesAdminPage.s10') }}</legend><label v-for="permission in permissions" :key="permission" class="admin-check"><input v-model="draft.permissions" type="checkbox" :value="permission"><span class="admin-check__text">{{ permissionLabels[permission] || permission }}</span></label></fieldset>
      </div>
      <div class="admin-form-actions"><AdminButton type="submit" variant="primary" :loading="busy" :disabled="loading || !draft.name.trim()">{{ $t('admin.RolesAdminPage.s11') }}</AdminButton><AdminButton v-if="editing" :disabled="busy" @click="edit()">{{ $t('admin.RolesAdminPage.s12') }}</AdminButton></div>
    </form>
    <section class="admin-card" :aria-busy="loading">
      <p v-if="loading" class="admin-empty" role="status">{{ $t('admin.RolesAdminPage.s13') }}</p>
      <AdminEmpty v-else-if="!error && !roles.length">{{ $t('admin.RolesAdminPage.s14') }}</AdminEmpty>
      <ul v-else class="admin-list">
        <AdminRow v-for="role in roles" :key="role.id" :title="role.name">
          <template #meta><AdminBadge v-if="role.protected" tone="info">{{ $t('admin.RolesAdminPage.s15') }}</AdminBadge><span>{{ role.protected ? $t('admin.RolesAdminPage.s16') : permissionText(role) }}</span></template>
          <AdminButton size="sm" :disabled="busy" @click="select(role)">{{ $t('admin.RolesAdminPage.s17') }}</AdminButton>
          <AdminButton size="sm" :disabled="busy || role.protected" @click="edit(role)">{{ $t('admin.RolesAdminPage.s18') }}</AdminButton>
          <AdminButton size="sm" variant="danger" :disabled="busy || role.protected" @click="remove(role)">{{ $t('admin.RolesAdminPage.s19') }}</AdminButton>
        </AdminRow>
      </ul>
    </section>
    <nav class="admin-pagination" :aria-label="$t('admin.RolesAdminPage.s20')">
      <AdminButton size="sm" :disabled="offset === 0 || loading || busy" @click="page(-1)">{{ $t('admin.RolesAdminPage.s21') }}</AdminButton>
      <span v-if="!loading && !error">{{ $t('admin.RolesAdminPage.s22', [total]) }}</span>
      <AdminButton size="sm" :disabled="offset + 40 >= total || loading || busy" @click="page(1)">{{ $t('admin.RolesAdminPage.s23') }}</AdminButton>
    </nav>
    <section v-if="selected" class="admin-card" :aria-label="$t('admin.RolesAdminPage.s24')" :aria-busy="memberLoading">
      <h2>{{ $t('admin.RolesAdminPage.s25', [selected.name]) }}</h2>
      <AdminInlineForm v-if="!selected.protected" v-model="accountID" :label="$t('admin.RolesAdminPage.s26')" :button-label="$t('admin.RolesAdminPage.s27')" inputmode="numeric" pattern="[0-9]+" :busy="busy || memberLoading" @submit="changeMember(accountID, true)" />
      <p v-if="memberLoading" class="admin-empty" role="status">{{ $t('admin.RolesAdminPage.s28') }}</p>
      <AdminAlert v-else-if="memberError" tone="danger">{{ memberError }}<template #action><AdminButton size="sm" @click="loadMembers">{{ $t('admin.RolesAdminPage.s29') }}</AdminButton></template></AdminAlert>
      <AdminEmpty v-else-if="!members.length">{{ $t('admin.RolesAdminPage.s30') }}</AdminEmpty>
      <ul v-else class="admin-list">
        <AdminRow v-for="member in members" :key="member.account_id" :title="`${member.username} · #${member.account_id}`">
          <AdminButton size="sm" variant="danger" :disabled="busy || selected.protected" @click="changeMember(member.account_id, false)">{{ $t('admin.RolesAdminPage.s31') }}</AdminButton>
        </AdminRow>
      </ul>
      <nav class="admin-pagination" :aria-label="$t('admin.RolesAdminPage.s32')">
        <AdminButton size="sm" :disabled="memberOffset === 0 || busy || memberLoading" @click="memberPage(-1)">{{ $t('admin.RolesAdminPage.s21') }}</AdminButton>
        <span v-if="!memberLoading && !memberError">{{ $t('admin.RolesAdminPage.s33', [memberTotal]) }}</span>
        <AdminButton size="sm" :disabled="memberOffset + 40 >= memberTotal || busy || memberLoading" @click="memberPage(1)">{{ $t('admin.RolesAdminPage.s23') }}</AdminButton>
      </nav>
    </section>
  </section>
</template>
