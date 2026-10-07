<script setup lang="ts">
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
  <section class="invites-page">
<header><h1>邀请管理</h1><button :disabled="busy || loading" @click="load()">刷新</button></header>
    <p>邀请码可用于现有注册流程。注册成功时原子扣减次数；停用不会影响已经注册的账号。</p>
    <p v-if="error" role="alert">{{ error }}</p><p v-if="notice" role="status">{{ notice }}</p>
    <form aria-label="生成邀请" @submit.prevent="create"><h2>生成邀请码</h2><label>数量<input v-model.number="draft.count" type="number" min="1" max="100" required></label><label>每个邀请码最多使用次数<input v-model.number="draft.max_uses" type="number" min="0" max="10000" required><span>0 表示不限次数</span></label><label>有效期<select v-model.number="draft.expires_in"><option :value="3600">1 小时</option><option :value="86400">1 天</option><option :value="604800">7 天</option><option :value="2592000">30 天</option><option :value="0">永不过期</option></select></label><button :disabled="busy || loading" type="submit">生成邀请</button></form>
    <form aria-label="邀请筛选" @submit.prevent="load(true)"><label>状态<select v-model="state"><option value="">全部</option><option value="active">可使用</option><option value="disabled">已停用</option><option value="expired">已过期</option><option value="exhausted">次数用尽</option></select></label><button :disabled="busy || loading">筛选</button></form>
    <p v-if="loading">正在加载…</p><p v-else-if="!rows.length">暂无符合条件的邀请码。</p>
    <article v-for="invite in rows" :key="invite.id"><header><code>{{ invite.code }}</code><span>{{ status(invite) }}</span></header><p>已使用 {{ invite.uses }} / {{ invite.max_uses === 0 ? '不限' : invite.max_uses }} 次 · 到期：{{ invite.expires_at ? new Date(invite.expires_at).toLocaleString() : '永不过期' }}</p><p>创建者账号 #{{ invite.account_id }}</p><div><button @click="copy(invite.code)">复制邀请码</button><button :disabled="busy" @click="toggle(invite)">{{ invite.disabled ? '启用' : '停用' }}</button></div></article>
    <nav aria-label="邀请分页"><button :disabled="offset === 0 || busy || loading" @click="page(-1)">上一页</button><span>共 {{ total }} 个邀请码</span><button :disabled="offset + 40 >= total || busy || loading" @click="page(1)">下一页</button></nav>
  </section>
</template>

<style scoped>
.invites-page{display:grid;gap:1rem;max-width:1100px;margin:auto;padding:1rem;overflow-wrap:anywhere}header,nav,article>div{display:flex;align-items:center;justify-content:space-between;gap:.75rem;flex-wrap:wrap}form,article{display:grid;gap:.8rem;border:1px solid var(--color-border,#d7dce3);border-radius:12px;padding:1rem}label{display:flex;align-items:center;gap:.7rem;flex-wrap:wrap}input,select{min-width:0;max-width:100%;padding:.5rem;border:1px solid #aab3c0;border-radius:6px}button{padding:.5rem .8rem;border:1px solid #aab3c0;border-radius:6px;cursor:pointer}button:disabled{opacity:.5;cursor:default}code{white-space:normal;word-break:break-all;user-select:all}[role=alert]{color:#b42318}[role=status]{color:#167047}h1,h2,p{margin:0}h2{font-size:1.1rem}
</style>
