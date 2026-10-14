<script setup lang="ts">
import { AdminAlert, AdminBadge, AdminButton, AdminCheck, AdminInlineForm, AdminEmpty, AdminField, AdminPageHeader, AdminRow } from "../components/ui"
import { onMounted, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { accessAdminApi as api, permissionLabels, type AdminRole, type RoleDraft, type RoleMember } from '../../infrastructure/accessAdminApi'
const roles = ref<AdminRole[]>([]), permissions = ref<string[]>([]), members = ref<RoleMember[]>([])
const selected = ref<AdminRole | null>(null), editing = ref<string | null>(null)
const offset = ref(0), total = ref(0), memberOffset = ref(0), memberTotal = ref(0)
const error = ref(''), notice = ref(''), accountID = ref(''), loading = ref(false), busy = ref(false)
const memberLoading = ref(false), memberError = ref('')
const draft = reactive<RoleDraft>({ name: '', color: '', highlighted: false, permissions: [] })
let alive = true, listVersion = 0, memberVersion = 0
onBeforeUnmount(() => { alive = false; listVersion++; memberVersion++ })
watch(sessionEpoch, () => { listVersion++; memberVersion++; roles.value = []; members.value = []; permissions.value = []; selected.value = null; editing.value = null; loading.value = false; busy.value = false; memberLoading.value = false; error.value = ''; memberError.value = ''; notice.value = ''; accountID.value = ''; total.value = 0; memberTotal.value = 0; edit() }, { flush: 'sync' })
async function load() { const version = ++listVersion; loading.value = true; error.value = ''; try { const data = await api.roles(offset.value); if (!alive || version !== listVersion) return; roles.value = data.items; total.value = data.total; permissions.value = data.available_permissions } catch { if (alive && version === listVersion) error.value = '无法加载角色，请重试。' } finally { if (alive && version === listVersion) loading.value = false } }
function edit(role?: AdminRole) { editing.value = role?.id ?? null; Object.assign(draft, { name: role?.name ?? '', color: role?.color ?? '', highlighted: role?.highlighted ?? false, permissions: [...(role?.permissions ?? [])] }); notice.value = '' }
async function save() { if (busy.value || loading.value) return; const epoch = sessionEpoch.value; busy.value = true; error.value = ''; notice.value = ''; try { await api.saveRole(editing.value, { ...draft, permissions: [...draft.permissions] }); if (!alive || epoch !== sessionEpoch.value) return; edit(); notice.value = '角色已保存，权限在后续请求中立即生效。'; await load() } catch { if (alive && epoch === sessionEpoch.value) error.value = '保存失败，请检查名称与权限。输入内容已保留。' } finally { if (alive && epoch === sessionEpoch.value) busy.value = false } }
async function remove(role: AdminRole) { if (busy.value) return; const epoch = sessionEpoch.value; if (!window.confirm(`删除角色“${role.name}”？所有成员将立即失去此角色授予的权限。`)) return; busy.value = true; error.value = ''; try { await api.deleteRole(role.id); if (!alive || epoch !== sessionEpoch.value) return; if (selected.value?.id === role.id) { selected.value = null; members.value = []; memberVersion++ } if (editing.value === role.id) edit(); await load() } catch { if (alive && epoch === sessionEpoch.value) error.value = '删除失败，角色可能受保护或权限不足。' } finally { if (alive && epoch === sessionEpoch.value) busy.value = false } }
async function loadMembers() {
  const role = selected.value
  if (!role) return
  const version = ++memberVersion
  memberLoading.value = true; memberError.value = ''
  try {
    const data = await api.members(role.id, memberOffset.value)
    if (alive && version === memberVersion) { members.value = data.items; memberTotal.value = data.total }
  } catch {
    if (alive && version === memberVersion) memberError.value = '无法加载成员，请重试。'
  } finally {
    if (alive && version === memberVersion) memberLoading.value = false
  }
}
function select(role: AdminRole) { selected.value = role; members.value = []; memberTotal.value = 0; memberOffset.value = 0; accountID.value = ''; void loadMembers() }
async function changeMember(account: string, assign: boolean) { const role = selected.value; if (!role || busy.value || memberLoading.value || !/^\d+$/.test(account)) return; const epoch = sessionEpoch.value; if (!window.confirm(assign ? '为该账号授予此角色的管理权限？' : '移除此账号的角色权限？')) return; busy.value = true; error.value = ''; try { await api.changeMember(role.id, account, assign); if (!alive || epoch !== sessionEpoch.value) return; accountID.value = ''; await loadMembers(); notice.value = '成员权限已更新。' } catch { if (alive && epoch === sessionEpoch.value) error.value = '操作失败：请确认本地账号 ID、授权范围，并勿修改自己的角色。' } finally { if (alive && epoch === sessionEpoch.value) busy.value = false } }
function permissionText(role: AdminRole) { return role.permissions.map(permission => permissionLabels[permission] || permission).join('、') || '无管理权限' }
function page(delta: number) { offset.value = Math.max(0, offset.value + delta * 40); void load() }
function memberPage(delta: number) { memberOffset.value = Math.max(0, memberOffset.value + delta * 40); void loadMembers() }
onMounted(load)
</script>

<template>
  <section class="admin-page">
    <AdminPageHeader title="角色与权限" description="分配管理能力；不能授予自己没有的权限。">
      <AdminButton size="sm" :disabled="loading || busy" @click="load">刷新</AdminButton>
    </AdminPageHeader>
    <AdminAlert v-if="error" tone="danger">{{ error }}</AdminAlert>
    <AdminAlert v-if="notice" tone="success">{{ notice }}</AdminAlert>
    <form class="admin-card" aria-label="角色编辑" @submit.prevent="save">
      <h2>{{ editing ? '编辑角色' : '创建角色' }}</h2>
      <div class="admin-form admin-form--columns">
        <AdminField label="名称"><template #default="{ id }"><input :id="id" v-model="draft.name" required maxlength="20"></template></AdminField>
        <AdminField label="颜色（可选）"><template #default="{ id }"><input :id="id" v-model="draft.color" placeholder="#2463eb" pattern="#[0-9a-fA-F]{6}"></template></AdminField>
        <AdminCheck v-model="draft.highlighted" label="在资料中突出显示" />
        <fieldset class="admin-check-group"><legend>授权能力</legend><label v-for="permission in permissions" :key="permission" class="admin-check"><input v-model="draft.permissions" type="checkbox" :value="permission"><span class="admin-check__text">{{ permissionLabels[permission] || permission }}</span></label></fieldset>
      </div>
      <div class="admin-form-actions"><AdminButton type="submit" variant="primary" :loading="busy" :disabled="loading || !draft.name.trim()">保存角色</AdminButton><AdminButton v-if="editing" :disabled="busy" @click="edit()">取消编辑</AdminButton></div>
    </form>
    <section class="admin-card" :aria-busy="loading">
      <p v-if="loading" class="admin-empty" role="status">正在加载…</p>
      <AdminEmpty v-else-if="!error && !roles.length">暂无角色。</AdminEmpty>
      <ul v-else class="admin-list">
        <AdminRow v-for="role in roles" :key="role.id" :title="role.name">
          <template #meta><AdminBadge v-if="role.protected" tone="info">系统角色</AdminBadge><span>{{ role.protected ? '保留系统角色，仅展示已有配置' : permissionText(role) }}</span></template>
          <AdminButton size="sm" :disabled="busy" @click="select(role)">查看成员</AdminButton>
          <AdminButton size="sm" :disabled="busy || role.protected" @click="edit(role)">编辑</AdminButton>
          <AdminButton size="sm" variant="danger" :disabled="busy || role.protected" @click="remove(role)">删除</AdminButton>
        </AdminRow>
      </ul>
    </section>
    <nav class="admin-pagination" aria-label="角色分页">
      <AdminButton size="sm" :disabled="offset === 0 || loading || busy" @click="page(-1)">上一页</AdminButton>
      <span v-if="!loading && !error">共 {{ total }} 个角色</span>
      <AdminButton size="sm" :disabled="offset + 40 >= total || loading || busy" @click="page(1)">下一页</AdminButton>
    </nav>
    <section v-if="selected" class="admin-card" aria-label="角色成员" :aria-busy="memberLoading">
      <h2>{{ selected.name }} · 成员</h2>
      <AdminInlineForm v-if="!selected.protected" v-model="accountID" label="本地账号 ID" button-label="添加成员" inputmode="numeric" pattern="[0-9]+" :busy="busy || memberLoading" @submit="changeMember(accountID, true)" />
      <p v-if="memberLoading" class="admin-empty" role="status">正在加载成员…</p>
      <AdminAlert v-else-if="memberError" tone="danger">{{ memberError }}<template #action><AdminButton size="sm" @click="loadMembers">重试</AdminButton></template></AdminAlert>
      <AdminEmpty v-else-if="!members.length">暂无成员。</AdminEmpty>
      <ul v-else class="admin-list">
        <AdminRow v-for="member in members" :key="member.account_id" :title="`${member.username} · #${member.account_id}`">
          <AdminButton size="sm" variant="danger" :disabled="busy || selected.protected" @click="changeMember(member.account_id, false)">移除成员</AdminButton>
        </AdminRow>
      </ul>
      <nav class="admin-pagination" aria-label="成员分页">
        <AdminButton size="sm" :disabled="memberOffset === 0 || busy || memberLoading" @click="memberPage(-1)">上一页</AdminButton>
        <span v-if="!memberLoading && !memberError">共 {{ memberTotal }} 人</span>
        <AdminButton size="sm" :disabled="memberOffset + 40 >= memberTotal || busy || memberLoading" @click="memberPage(1)">下一页</AdminButton>
      </nav>
    </section>
  </section>
</template>
