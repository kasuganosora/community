<script setup lang="ts">
import { AdminAlert, AdminBadge, AdminButton, AdminEmpty, AdminField, AdminPageHeader, AdminRow } from "../components/ui"
import { onMounted, onBeforeUnmount, reactive, ref } from 'vue'
import { accessAdminApi as api, type AdminInvite } from '../../infrastructure/accessAdminApi'
const rows = ref<AdminInvite[]>([]), total = ref(0), offset = ref(0), state = ref('')
const error = ref(''), notice = ref(''), loading = ref(false), busy = ref(false)
const draft = reactive({ count: 1, max_uses: 1, expires_in: 604800 })
let alive = true, version = 0
onBeforeUnmount(() => { alive = false; version++ })
async function load(reset = false) { if (reset) offset.value = 0; const request = ++version; loading.value = true; error.value = ''; try { const result = await api.invites(state.value, offset.value); if (alive && request === version) { rows.value = result.items; total.value = result.total } } catch { if (alive && request === version) error.value = '无法加载邀请，请重试。' } finally { if (alive && request === version) loading.value = false } }
async function create() { if (busy.value) return; busy.value = true; error.value = ''; notice.value = ''; try { const result = await api.createInvites({ ...draft }); if (!alive) return; notice.value = `已创建 ${result.items.length} 个邀请码，请仅分享给受邀者。`; state.value = ''; await load(true) } catch { if (alive) error.value = '生成失败，设置已保留；请刷新确认后重试，避免重复创建。' } finally { if (alive) busy.value = false } }
async function toggle(invite: AdminInvite) { if (!window.confirm(invite.disabled ? '重新启用此邀请码？过期或次数用尽的邀请仍不可用。' : '停用此邀请码？此后注册将无法消费该邀请码。')) return; busy.value = true; error.value = ''; try { await api.setInviteDisabled(invite.id, !invite.disabled); if (alive) await load() } catch { if (alive) error.value = '更新失败，请刷新确认当前状态。' } finally { if (alive) busy.value = false } }
async function copy(code: string) { try { await navigator.clipboard.writeText(code); if (alive) notice.value = '邀请码已复制。' } catch { if (alive) error.value = '无法访问剪贴板，请手动选择并复制邀请码。' } }
function status(invite: AdminInvite) { if (invite.disabled) return '已停用'; if (invite.expires_at && Date.parse(invite.expires_at) <= Date.now()) return '已过期'; if (invite.max_uses > 0 && invite.uses >= invite.max_uses) return '已用尽'; return invite.available ? '可使用' : '不可使用' }
function page(delta: number) { offset.value = Math.max(0, offset.value + delta * 40); void load() }
onMounted(() => load())
</script>

<template>
  <section class="admin-page">
    <AdminPageHeader title="邀请管理" description="生成邀请码，控制注册入口。">
      <AdminButton size="sm" :disabled="busy || loading" @click="load()">刷新</AdminButton>
    </AdminPageHeader>
    <AdminAlert v-if="error" tone="danger">{{ error }}</AdminAlert>
    <AdminAlert v-if="notice" tone="success">{{ notice }}</AdminAlert>
    <form class="admin-card" aria-label="生成邀请" @submit.prevent="create">
      <h2>生成邀请码</h2>
      <div class="admin-toolbar">
        <AdminField label="数量"><template #default="{ id }"><input :id="id" v-model.number="draft.count" type="number" min="1" max="100" required></template></AdminField>
        <AdminField label="最多使用次数" hint="0 表示不限次数"><template #default="{ id }"><input :id="id" v-model.number="draft.max_uses" type="number" min="0" max="10000" required></template></AdminField>
        <AdminField label="有效期"><template #default="{ id }"><select :id="id" v-model.number="draft.expires_in"><option :value="3600">1 小时</option><option :value="86400">1 天</option><option :value="604800">7 天</option><option :value="2592000">30 天</option><option :value="0">永不过期</option></select></template></AdminField>
      </div>
      <div class="admin-form-actions"><AdminButton type="submit" variant="primary" :disabled="busy || loading">生成邀请</AdminButton></div>
    </form>
    <form class="admin-toolbar" aria-label="邀请筛选" @submit.prevent="load(true)">
      <AdminField label="状态"><template #default="{ id }"><select :id="id" v-model="state"><option value="">全部</option><option value="active">可使用</option><option value="disabled">已停用</option><option value="expired">已过期</option><option value="exhausted">次数用尽</option></select></template></AdminField>
      <AdminButton type="submit" :disabled="busy || loading">筛选</AdminButton>
    </form>
    <section class="admin-card" :aria-busy="loading">
      <p v-if="loading" class="admin-empty" role="status">正在加载…</p>
      <AdminEmpty v-else-if="!error && !rows.length">暂无符合条件的邀请码。</AdminEmpty>
      <ul v-else class="admin-list">
        <AdminRow v-for="invite in rows" :key="invite.id" :title="invite.code" mono>
          <template #meta>
            <AdminBadge :tone="status(invite) === '可使用' ? 'success' : 'neutral'">{{ status(invite) }}</AdminBadge>
            <span>已使用 {{ invite.uses }} / {{ invite.max_uses === 0 ? '不限' : invite.max_uses }} 次</span>
            <span>到期：{{ invite.expires_at ? new Date(invite.expires_at).toLocaleString() : '永不过期' }}</span>
            <span>创建者 #{{ invite.account_id }}</span>
          </template>
          <AdminButton size="sm" @click="copy(invite.code)">复制邀请码</AdminButton>
          <AdminButton size="sm" :variant="invite.disabled ? 'secondary' : 'danger'" :disabled="busy" @click="toggle(invite)">{{ invite.disabled ? '启用' : '停用' }}</AdminButton>
        </AdminRow>
      </ul>
    </section>
    <nav class="admin-pagination" aria-label="邀请分页">
      <AdminButton size="sm" :disabled="offset === 0 || busy || loading" @click="page(-1)">上一页</AdminButton>
      <span>共 {{ total }} 个邀请码</span>
      <AdminButton size="sm" :disabled="offset + 40 >= total || busy || loading" @click="page(1)">下一页</AdminButton>
    </nav>
  </section>
</template>
