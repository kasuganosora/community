<script setup lang="ts">
import RichText from '@/shared/components/common/RichText.vue'
import { onMounted, onBeforeUnmount, reactive, ref } from 'vue'
import { moderationAdminApi as api, type AdminAccount, type AccountAction } from '../../infrastructure/moderationAdminApi'
const filters = reactive({ origin: '', status: '', username: '', display_name: '' })
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
const names: Record<AccountAction, string> = { disable: '停用登录', enable: '恢复登录', suspend: '封禁账号', unsuspend: '解除封禁', approve: '批准注册', reject: '拒绝注册' }
function state(a: AdminAccount) {
  if (a.suspended) return '已封禁'
  if (a.user_status === 1) return '待审核'
  if (a.user_status === 2 || a.disabled) return '已停用'
  if (a.user_status === 3) return '待验证邮箱'
  return '正常'
}
function message(e: unknown) { return e instanceof Error ? e.message : '请求失败，请重试' }
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
async function detail(id: string) {
  const version = ++detailRequest
  selected.value = null
  detailLoading.value = true
  error.value = ''
  try { const value = await api.account(id); if (version === detailRequest) selected.value = value }
  catch (e) { if (version === detailRequest) error.value = message(e) }
  finally { if (version === detailRequest) detailLoading.value = false }
}
async function act(action: AccountAction) {
  const account = selected.value
  if (!account || busy.value || !window.confirm(`确认对 @${account.username}${account.domain ? `@${account.domain}` : ''} 执行“${names[action]}”？`)) return
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
      <h1>用户管理</h1>
      <p>查看账号，处理注册审核与封禁。</p>
    </header>
    <form class="admin-toolbar" aria-label="搜索账号" @submit.prevent="!busy && load(true)">
      <label class="admin-field">用户名<input v-model="filters.username" /></label>
      <label class="admin-field">显示名称<input v-model="filters.display_name" /></label>
      <label class="admin-field">来源<select v-model="filters.origin"><option value="">全部</option><option value="local">本地</option><option value="remote">远程</option></select></label>
      <label class="admin-field">状态<select v-model="filters.status"><option value="">全部</option><option value="active">正常</option><option value="pending">待审核</option><option value="disabled">已停用</option><option value="suspended">已封禁</option></select></label>
      <button class="admin-button admin-button--primary" :disabled="loading || busy">搜索</button>
    </form>
    <p v-if="error" class="admin-error" role="alert">{{ error }}</p>
    <p v-if="loading" class="admin-empty" role="status">正在加载账号…</p>
    <div class="admin-table-wrap admin-card" role="region" aria-label="账号列表，可横向滚动" tabindex="0">
      <table class="admin-table">
        <thead><tr><th>账号</th><th>来源</th><th>状态</th><th>角色</th><th>操作</th></tr></thead>
        <tbody><tr v-for="a in items" :key="a.id"><td>{{ a.display_name || a.username }}<br /><small>@{{ a.username }}{{ a.domain ? `@${a.domain}` : '' }}</small></td><td>{{ a.domain || '本地' }}</td><td><span class="admin-badge">{{ state(a) }}</span></td><td>{{ a.role === 'admin' ? '管理员' : a.role === 'user' ? '用户' : '—' }}</td><td><button class="admin-button" :disabled="loading || busy" @click="detail(a.id)">详情</button></td></tr></tbody>
      </table>
      <p v-if="!loading && !error && !items.length" class="admin-empty">没有符合条件的账号</p>
    </div>
    <nav class="admin-pagination" aria-label="账号分页">
      <button class="admin-button" :disabled="loading || busy || offset === 0" @click="page(-1)">上一页</button>
      <span>第 {{ Math.floor(offset / limit) + 1 }} 页{{ total !== null ? ` · 共 ${total} 个账号` : '' }}</span>
      <button class="admin-button" :disabled="loading || busy || (total !== null ? offset + limit >= total : items.length < limit)" @click="page(1)">下一页</button>
    </nav>
    <p v-if="detailLoading" class="admin-empty" role="status">正在加载详情…</p>
    <section v-if="selected" class="admin-card" aria-label="账号详情">
      <h2>@{{ selected.username }}{{ selected.domain ? `@${selected.domain}` : '' }}</h2>
      <dl><dt>账号 ID</dt><dd>{{ selected.id }}</dd><dt>显示名称</dt><dd>{{ selected.display_name || selected.username }}</dd><dt>状态</dt><dd>{{ state(selected) }}</dd><dt>角色</dt><dd>{{ selected.role === 'admin' ? '管理员' : selected.role === 'user' ? '用户' : '—' }}</dd><dt>创建时间</dt><dd>{{ selected.created_at ? new Date(selected.created_at).toLocaleString() : '—' }}</dd><dt>关注审核</dt><dd>{{ selected.locked ? '已开启' : '未开启' }}</dd></dl>
      <RichText v-if="selected.account?.note" :html="selected.account.note" :emojis="selected.account.emojis" />
      <div class="admin-toolbar">
        <template v-if="!selected.domain && selected.user_status !== undefined">
          <template v-if="selected.user_status === 1"><button class="admin-button admin-button--primary" :disabled="busy" @click="act('approve')">批准注册</button><button class="admin-button admin-button--danger" :disabled="busy" @click="act('reject')">拒绝注册</button></template>
          <button v-else-if="selected.user_status === 2 || selected.disabled" class="admin-button" :disabled="busy" @click="act('enable')">恢复登录</button>
          <button v-else class="admin-button admin-button--danger" :disabled="busy" @click="act('disable')">停用登录</button>
        </template>
        <button v-if="selected.suspended" class="admin-button" :disabled="busy" @click="act('unsuspend')">解除封禁</button>
        <button v-else class="admin-button admin-button--danger" :disabled="busy" @click="act('suspend')">封禁账号</button>
      </div>
    </section>
  </section>
</template>
