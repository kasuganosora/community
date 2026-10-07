<script setup lang="ts">
import { onMounted, onBeforeUnmount, reactive, ref } from 'vue'
import { accessAdminApi as api, permissionLabels, type AdminRole, type RoleDraft, type RoleMember } from '../../infrastructure/accessAdminApi'
const roles = ref<AdminRole[]>([]), permissions = ref<string[]>([]), members = ref<RoleMember[]>([])
const selected = ref<AdminRole | null>(null), editing = ref<string | null>(null)
const offset = ref(0), total = ref(0), memberOffset = ref(0), memberTotal = ref(0)
const error = ref(''), notice = ref(''), accountID = ref(''), loading = ref(false), busy = ref(false)
const draft = reactive<RoleDraft>({ name: '', color: '', highlighted: false, permissions: [] })
let alive = true, listVersion = 0, memberVersion = 0
onBeforeUnmount(() => { alive = false; listVersion++; memberVersion++ })
async function load() { const version = ++listVersion; loading.value = true; error.value = ''; try { const data = await api.roles(offset.value); if (!alive || version !== listVersion) return; roles.value = data.items; total.value = data.total; permissions.value = data.available_permissions } catch { if (alive) error.value = '无法加载角色，请重试。' } finally { if (alive && version === listVersion) loading.value = false } }
function edit(role?: AdminRole) { editing.value = role?.id ?? null; Object.assign(draft, { name: role?.name ?? '', color: role?.color ?? '', highlighted: role?.highlighted ?? false, permissions: [...(role?.permissions ?? [])] }); notice.value = '' }
async function save() { if (busy.value) return; busy.value = true; error.value = ''; notice.value = ''; try { await api.saveRole(editing.value, { ...draft, permissions: [...draft.permissions] }); if (!alive) return; edit(); notice.value = '角色已保存，权限在后续请求中立即生效。'; await load() } catch { if (alive) error.value = '保存失败，请检查名称与权限。输入内容已保留。' } finally { if (alive) busy.value = false } }
async function remove(role: AdminRole) { if (!window.confirm(`删除角色“${role.name}”？所有成员将立即失去此角色授予的权限。`)) return; busy.value = true; error.value = ''; try { await api.deleteRole(role.id); if (!alive) return; if (selected.value?.id === role.id) { selected.value = null; members.value = []; memberVersion++ } if (editing.value === role.id) edit(); await load() } catch { if (alive) error.value = '删除失败，角色可能受保护或权限不足。' } finally { if (alive) busy.value = false } }
async function loadMembers() { const role = selected.value; if (!role) return; const version = ++memberVersion; try { const data = await api.members(role.id, memberOffset.value); if (alive && version === memberVersion) { members.value = data.items; memberTotal.value = data.total } } catch { if (alive && version === memberVersion) error.value = '无法加载成员，请重试。' } }
function select(role: AdminRole) { selected.value = role; members.value = []; memberOffset.value = 0; accountID.value = ''; void loadMembers() }
async function changeMember(account: string, assign: boolean) { const role = selected.value; if (!role || busy.value || !/^\d+$/.test(account)) return; if (!window.confirm(assign ? '为该账号授予此角色的管理权限？' : '移除此账号的角色权限？')) return; busy.value = true; error.value = ''; try { await api.changeMember(role.id, account, assign); if (!alive) return; accountID.value = ''; await loadMembers(); notice.value = '成员权限已更新。' } catch { if (alive) error.value = '操作失败：请确认本地账号 ID、授权范围，并勿修改自己的角色。' } finally { if (alive) busy.value = false } }
function permissionText(role: AdminRole) { return role.permissions.map(permission => permissionLabels[permission] || permission).join('、') || '无管理权限' }
function page(delta: number) { offset.value = Math.max(0, offset.value + delta * 40); void load() }
function memberPage(delta: number) { memberOffset.value = Math.max(0, memberOffset.value + delta * 40); void loadMembers() }
onMounted(load)
</script>

<template>
  <section class="access-page">
    <header><div><h1>角色与权限</h1><p>分配实际管理能力。普通角色不能修改超级管理员身份或授予自己没有的权限。</p></div><button :disabled="loading || busy" @click="load">刷新</button></header>
    <p v-if="error" role="alert">{{ error }}</p><p v-if="notice" role="status">{{ notice }}</p>
    <form aria-label="角色编辑" @submit.prevent="save">
      <h2>{{ editing ? '编辑角色' : '创建角色' }}</h2>
      <label>名称<input v-model="draft.name" required maxlength="20"></label>
      <label>颜色（可选）<input v-model="draft.color" placeholder="#2463eb" pattern="#[0-9a-fA-F]{6}"></label>
      <label><input v-model="draft.highlighted" type="checkbox">在资料中突出显示</label>
      <fieldset><legend>授权能力</legend><label v-for="permission in permissions" :key="permission"><input v-model="draft.permissions" type="checkbox" :value="permission">{{ permissionLabels[permission] || permission }}</label></fieldset>
      <div><button :disabled="busy || loading || !draft.name.trim()" type="submit">保存角色</button><button v-if="editing" :disabled="busy" type="button" @click="edit()">取消编辑</button></div>
    </form>
    <p v-if="loading">正在加载…</p><p v-else-if="!roles.length">暂无角色。</p>
    <article v-for="role in roles" :key="role.id"><h2>{{ role.name }}</h2><p>{{ role.protected ? '保留系统角色，仅展示已有配置' : permissionText(role) }}</p><div><button :disabled="busy" @click="select(role)">查看成员</button><button :disabled="busy || role.protected" @click="edit(role)">编辑</button><button :disabled="busy || role.protected" @click="remove(role)">删除</button></div></article>
    <nav aria-label="角色分页"><button :disabled="offset === 0 || loading || busy" @click="page(-1)">上一页</button><span>共 {{ total }} 个角色</span><button :disabled="offset + 40 >= total || loading || busy" @click="page(1)">下一页</button></nav>
    <section v-if="selected" aria-label="角色成员"><h2>{{ selected.name }} · 成员</h2><form v-if="!selected.protected" @submit.prevent="changeMember(accountID, true)"><label>本地账号 ID<input v-model="accountID" required inputmode="numeric" pattern="[0-9]+"></label><button :disabled="busy || !accountID">添加成员</button></form><p v-if="!members.length">暂无成员。</p><article v-for="member in members" :key="member.account_id"><span>{{ member.username }} · #{{ member.account_id }}</span><button :disabled="busy || selected.protected" @click="changeMember(member.account_id, false)">移除成员</button></article><nav aria-label="成员分页"><button :disabled="memberOffset === 0 || busy" @click="memberPage(-1)">上一页</button><span>共 {{ memberTotal }} 人</span><button :disabled="memberOffset + 40 >= memberTotal || busy" @click="memberPage(1)">下一页</button></nav></section>
  </section>
</template>

<style scoped>
.access-page{display:grid;gap:1rem;max-width:1100px;margin:auto;padding:1rem;overflow-wrap:anywhere}header,nav,article>div{display:flex;align-items:center;justify-content:space-between;gap:.75rem;flex-wrap:wrap}form,article,section[aria-label]{border:1px solid var(--color-border,#d7dce3);padding:1rem;border-radius:12px;display:grid;gap:.75rem}label{display:flex;align-items:center;gap:.5rem;flex-wrap:wrap}input:not([type=checkbox]){min-width:0;max-width:100%;padding:.55rem;border:1px solid #aab3c0;border-radius:6px}fieldset{display:flex;gap:.8rem;flex-wrap:wrap;border:1px solid #aab3c0}button{padding:.5rem .8rem;border:1px solid #aab3c0;border-radius:6px;cursor:pointer}button:disabled{opacity:.5;cursor:default}[role=alert]{color:#b42318}[role=status]{color:#167047}h1,h2,p{margin:0}h2{font-size:1.1rem}
</style>
