<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount, watch } from 'vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { discoveryAdminApi as api, type DiscoveryAdminStatus, type DiscoverySuppression } from '../../infrastructure/discoveryAdminApi'
import { invalidateDiscovery } from '@/domains/discovery/presentation/composables/discoveryState'
import type { TargetKind } from '@/domains/discovery/infrastructure/discoveryApi'
const status = ref<DiscoveryAdminStatus | null>(null), rows = ref<DiscoverySuppression[]>([])
const loading = ref(false), busy = ref(false), error = ref(''), success = ref('')
const kind = ref<TargetKind>('account'), target = ref(''), reason = ref('')
const kindLabel = { account: '作者', status: '帖子', tag: '话题' }
const modeLabel: Record<string, string> = { rules: '基础规则', meili_lexical: '内容检索增强', meili_hybrid: '语义检索增强', disabled: '已关闭', chronological_fallback: '近期公开帖降级' }
const reasonLabel: Record<string, string> = {
  not_built: '等待构建公开内容索引', not_initialized: '内容增强尚未初始化', meilisearch_disabled: '未启用 Meilisearch，使用基础规则',
  recommendations_disabled: '内容发现已关闭', settings_unavailable: '暂时无法读取配置', rules_configured: '管理员已选择基础规则',
  build_unavailable: '索引构建暂时不可用', query_unavailable: '内容检索暂时不可用', built: '索引已构建，等待就绪检查', ready: '可以使用',
  resource_budget_exceeded: '索引资源用量达到配置上限',
  redis_unavailable: '缓存暂时不可用', configuration_changed: '配置已改变，需要重建并重新验收', index_expired: '索引已过期，等待更新',
  quality_not_approved: '尚未批准当前版本的质量验收', approval_required: '等待当前版本的质量验收批准',
  pool_not_built: '候选池尚未构建', invalid_pool: '候选池无效，等待重建', pool_expired: '候选池已过期，等待更新',
}
function reasonText(code?: string) { return code ? reasonLabel[code] || '当前状态需要进一步检查服务日志' : '正常' }
function bytes(value: number) { return value >= 1048576 ? `${(value / 1048576).toFixed(1)} MiB` : `${(value / 1024).toFixed(1)} KiB` }
let version = 0
function date(value?: string) { return value && !value.startsWith('0001-') ? new Date(value).toLocaleString() : '暂无记录' }
async function load() {
  const mine = ++version, epoch = sessionEpoch.value
  loading.value = true; error.value = ''
  try {
    const [runtime, rules] = await Promise.all([api.status(), api.feedback()])
    if (mine !== version || epoch !== sessionEpoch.value) return
    status.value = runtime; rows.value = rules
  } catch (e) { if (mine === version) error.value = e instanceof Error ? e.message : '读取发现管理信息失败' }
  finally { if (mine === version) loading.value = false }
}
async function suppress() {
  if (busy.value || !target.value.trim() || !reason.value.trim()) return
  busy.value = true; error.value = ''; success.value = ''
  const epoch = sessionEpoch.value
  try {
    await api.suppress({ target_kind: kind.value, target_key: target.value.trim(), reason: reason.value.trim() })
    if (epoch !== sessionEpoch.value) return
    target.value = ''; reason.value = ''; invalidateDiscovery(); await load(); success.value = '已停止主动推荐。原有授权访问不受影响。'
  } catch (e) { if (epoch === sessionEpoch.value) error.value = e instanceof Error ? e.message : '保存禁推规则失败' }
  finally { if (epoch === sessionEpoch.value) busy.value = false }
}
async function restore(row: DiscoverySuppression) {
  if (busy.value) return
  busy.value = true; error.value = ''; success.value = ''
  const epoch = sessionEpoch.value
  try {
    await api.restore(row.id)
    if (epoch !== sessionEpoch.value) return
    rows.value = rows.value.filter(item => item.id !== row.id); invalidateDiscovery(); success.value = '已撤销站级禁推规则。'
  } catch (e) { if (epoch === sessionEpoch.value) error.value = e instanceof Error ? e.message : '撤销失败' }
  finally { if (epoch === sessionEpoch.value) busy.value = false }
}
watch(sessionEpoch, () => { ++version; status.value = null; rows.value = []; busy.value = false; success.value = ''; void load() }, { flush: 'sync' })
onMounted(load)
onBeforeUnmount(() => { ++version })
</script>

<template>
  <div class="admin-page admin-discovery">
    <header><h1>内容发现</h1><p>查看实际推荐模式，管理独立于封禁与删除的站级推荐规则。</p></header>
    <p v-if="error" class="admin-error" role="alert">{{ error }}</p>
    <p v-if="success" class="admin-notice" role="status">{{ success }}</p>
    <section class="admin-card" :aria-busy="loading">
      <div class="discovery-admin-heading"><h2>运行状态</h2><button type="button" class="admin-button" :disabled="loading || busy" @click="load">{{ loading ? '读取中…' : '刷新' }}</button></div>
      <dl v-if="status" class="discovery-admin-stats">
        <div><dt>帖子与账号发现</dt><dd>{{ status.enabled ? '已开放' : '已关闭' }}</dd></div>
        <div><dt>热点话题</dt><dd>{{ status.trends_enabled ? '已开放' : '已关闭' }}</dd></div>
        <div><dt>实际算法</dt><dd>{{ !status.enabled ? '已关闭' : modeLabel[status.index.mode || 'rules'] || status.index.mode }}</dd></div>
        <div><dt>基础规则版本</dt><dd>{{ status.algorithm_version }}</dd></div>
        <div><dt>内容索引</dt><dd>{{ status.index.ready ? '已就绪' : '未就绪' }}<small>{{ reasonText(status.index.reason) }}</small></dd></div>
        <div v-if="status.index.last_build_error"><dt>最近构建异常</dt><dd>{{ reasonText(status.index.last_build_error) }}<small>{{ date(status.index.last_build_error_at) }}</small></dd></div>
        <div v-if="status.index.last_query_error"><dt>最近检索异常</dt><dd>{{ reasonText(status.index.last_query_error) }}<small>{{ date(status.index.last_query_error_at) }}</small></dd></div>
        <div><dt>索引公开文档数</dt><dd>{{ status.index.document_count }}</dd></div>
        <div><dt>索引数据截止</dt><dd>{{ date(status.index.data_cutoff) }}</dd></div>
        <div><dt>最近检查</dt><dd>{{ date(status.index.checked_at) }}</dd></div>
        <div><dt>当前增强版本</dt><dd>{{ status.index.version || '暂无可用版本' }}</dd></div>
        <div><dt>质量批准版本</dt><dd>{{ status.approved_version || '尚未批准增强模式' }}</dd></div>
      </dl>
      <p class="admin-hint">索引就绪不等于质量已通过。增强版本须完成既定验收后批准；未批准时保持基础规则。</p>
      <router-link class="admin-button" to="/admin/settings/performance">配置开关与增强模式</router-link>
    </section>
    <section v-if="status?.runtime" class="admin-card">
      <h2>候选与缓存</h2>
      <p class="admin-hint">{{ reasonText(status.runtime.reason) }}。这里只显示运行汇总，不展示用户兴趣。</p>
      <dl class="discovery-admin-stats">
        <div><dt>共享候选</dt><dd>{{ status.runtime.pool_candidates }} 条 · {{ status.runtime.pool_stale ? '需要更新' : '有效' }}<small v-if="status.runtime.pool_truncated">本轮达到扫描预算，统计未覆盖全部内容。</small></dd></div>
        <div><dt>候选数据截止</dt><dd>{{ date(status.runtime.pool_as_of) }}</dd></div>
        <div><dt>候选载荷</dt><dd>{{ bytes(status.runtime.pool_payload_bytes) }}<small>仅载荷大小，不代表 Redis 实际内存。</small></dd></div>
        <div><dt>后台刷新</dt><dd>{{ status.runtime.pool_lease_active ? '正在刷新' : '等待下一轮' }}</dd></div>
        <div><dt>快照预留数量</dt><dd>{{ status.runtime.snapshot_reservations }} / {{ status.runtime.snapshot_maximum }}</dd></div>
        <div><dt>快照预留预算</dt><dd>{{ bytes(status.runtime.snapshot_reserved_bytes) }} / {{ bytes(status.runtime.cache_budget_bytes) }}<small>保守准入估算，不是实际内存测量。</small></dd></div>
      </dl>
    </section>
    <section class="admin-card">
      <h2>停止主动推荐</h2><p class="admin-hint">使用本站记录的账号 ID、帖子 ID 或不带 # 的话题名。此操作不删除内容，也不会屏蔽用户。</p>
      <form class="discovery-admin-form" @submit.prevent="suppress">
        <label class="admin-field">对象类型<select v-model="kind" :disabled="busy"><option value="account">作者</option><option value="status">帖子</option><option value="tag">话题</option></select></label>
        <label class="admin-field">{{ kindLabel[kind] }}{{ kind === 'tag' ? '名' : ' ID' }}<input v-model="target" required maxlength="255" :disabled="busy" :placeholder="kind === 'tag' ? '例如 photography' : '本站记录 ID'" /></label>
        <label class="admin-field discovery-admin-form__reason">管理原因<textarea v-model="reason" rows="2" required maxlength="500" :disabled="busy" placeholder="说明停止推荐的原因，操作会写入审计日志。" /></label>
        <button type="submit" class="admin-button admin-button--primary" :disabled="busy || !target.trim() || !reason.trim()">停止推荐</button>
      </form>
    </section>
    <section class="admin-card" :aria-busy="loading">
      <h2>站级禁推规则</h2>
      <p v-if="!rows.length && !loading" class="admin-empty">还没有站级禁推规则。</p>
      <ul v-else class="discovery-admin-rules"><li v-for="row in rows" :key="row.id"><div><strong>{{ kindLabel[row.target_kind] }} · {{ row.target_key }}</strong><p>{{ row.reason || '未附原因' }}</p><small>{{ date(row.created_at) }}</small></div><button type="button" class="admin-button" :disabled="busy" @click="restore(row)">撤销</button></li></ul>
    </section>
  </div>
</template>

<style scoped>
.discovery-admin-heading { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 1rem; }
.discovery-admin-stats { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 0 2rem; }
.discovery-admin-stats > div { padding: 1rem 0; border-bottom: 1px solid var(--color-separator); }
.discovery-admin-stats dt { color: var(--color-label-secondary); font-size: .8125rem; margin-bottom: .375rem; }
.discovery-admin-stats dd { margin: 0; overflow-wrap: anywhere; }
.discovery-admin-stats small { display: block; margin-top: .375rem; color: var(--color-label-secondary); }
.discovery-admin-form { display: grid; grid-template-columns: minmax(8rem, 1fr) minmax(10rem, 2fr); gap: 1rem; padding-top: 1rem; }
.discovery-admin-form__reason { grid-column: 1 / -1; }
.discovery-admin-form > button { justify-self: start; }
.discovery-admin-rules { list-style: none; padding: 0; }
.discovery-admin-rules li { display: flex; align-items: center; justify-content: space-between; gap: 1rem; padding: 1rem 0; border-bottom: 1px solid var(--color-separator); }
.discovery-admin-rules li > div { min-width: 0; overflow-wrap: anywhere; }
.discovery-admin-rules p, .discovery-admin-rules small { color: var(--color-label-secondary); margin-top: .375rem; }
.discovery-admin-rules button { flex-shrink: 0; }
@media (max-width: 699px) {
  .discovery-admin-stats { gap: 0 1rem; }
  .discovery-admin-form { grid-template-columns: minmax(0, 1fr); }
}
@media (max-width: 359px) { .discovery-admin-stats { grid-template-columns: minmax(0, 1fr); } }
</style>
