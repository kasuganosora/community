<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import AnalyticsOverview from '../components/AnalyticsOverview.vue'
import { dashboardApi, type AdminMeasure } from '../../infrastructure/dashboardApi'
import { instanceApi, type InstanceInfo } from '@/domains/instance/infrastructure/instanceApi'
const measures = ref<AdminMeasure[] | null>(null)
const instance = ref<InstanceInfo | null>(null)
const loading = ref(false), error = ref(''), instanceError = ref('')
const updated = ref('')
function value(key: string) { return measures.value?.find(item => item.key === key)?.total ?? '—' }
const unresolved = computed(() => {
  const opened = Number(value('opened_reports')), resolved = Number(value('resolved_reports'))
  return Number.isFinite(opened) && Number.isFinite(resolved) ? String(Math.max(0, opened - resolved)) : '—'
})
const cards = computed(() => [
  { title: '本地用户', value: value('user_count'), hint: '本站账户总数', icon: 'usergroup', color: 'blue' },
  { title: '收录帖子', value: value('status_count'), hint: '本站及已收录的联邦帖子', icon: 'chat', color: 'purple' },
  { title: '新增用户', value: value('new_users'), hint: '最近 30 天注册', icon: 'user-add', color: 'green' },
  { title: '待处理举报', value: unresolved.value, hint: '尚未完成处理', icon: 'flag', color: 'orange' },
])
async function load() {
  if (loading.value) return
  loading.value = true; error.value = ''; instanceError.value = ''; measures.value = null
  const [stats, info] = await Promise.allSettled([dashboardApi.measures(), instanceApi.getInstance()])
  if (stats.status === 'fulfilled') { measures.value = stats.value; updated.value = new Date().toLocaleTimeString('zh-CN') }
  else error.value = stats.reason instanceof Error ? stats.reason.message : '无法加载统计，请重试'
  if (info.status === 'fulfilled') instance.value = info.value
  else instanceError.value = '站点信息暂时无法加载'
  loading.value = false
}
onMounted(load)
</script>

<template>
  <div class="admin-page" :aria-busy="loading">
    <header class="admin-page-header"><div><h1>社区概览</h1><p>社区近况与待办。</p></div><button type="button" class="admin-button" :disabled="loading" @click="load"><t-icon name="refresh" aria-hidden="true" />{{ loading ? '正在更新…' : '刷新数据' }}</button></header>
    <p v-if="error" role="alert" class="admin-error">{{ error }} <button type="button" class="admin-button" :disabled="loading" @click="load">重试</button></p>
    <section class="overview-metrics" aria-label="社区统计">
      <article v-for="card in cards" :key="card.title" class="admin-card overview-metric"><div class="overview-metric-top"><h2>{{ card.title }}</h2><span class="overview-metric-icon" :class="`overview-metric-icon--${card.color}`"><t-icon :name="card.icon" :size="20" aria-hidden="true" /></span></div><strong>{{ loading ? '—' : card.value }}</strong><p>{{ card.hint }}</p></article>
    </section>
    <div class="overview-columns">
      <section class="admin-card overview-tasks"><div class="overview-section-heading"><h2>管理工作台</h2><span class="admin-badge">日常管理</span></div><router-link to="/admin/reports" class="overview-task"><span class="overview-task-icon"><t-icon name="flag" :size="23" aria-hidden="true" /></span><span><strong>关注社区反馈</strong><small>{{ unresolved === '—' ? '查看举报与处理记录' : `${unresolved} 条举报等待处理` }}</small></span><t-icon name="chevron-right" aria-hidden="true" /></router-link><router-link to="/admin/accounts" class="overview-task"><span class="overview-task-icon"><t-icon name="usergroup" :size="23" aria-hidden="true" /></span><span><strong>管理社区成员</strong><small>查找用户，审核申请与管理账户</small></span><t-icon name="chevron-right" aria-hidden="true" /></router-link><router-link to="/admin/emojis" class="overview-task"><span class="overview-task-icon"><t-icon name="happy" :size="23" aria-hidden="true" /></span><span><strong>丰富社区表达</strong><small>上传表情，维护分类与别名</small></span><t-icon name="chevron-right" aria-hidden="true" /></router-link></section>
      <section class="admin-card overview-instance"><div class="overview-section-heading"><h2>你的社区</h2><t-icon name="internet" :size="20" aria-hidden="true" /></div><p v-if="instanceError" role="alert" class="admin-error">{{ instanceError }}</p><template v-if="instance"><strong class="overview-instance-title">{{ instance.title || 'Community' }}</strong><p class="overview-domain">{{ instance.domain || instance.uri || '—' }}</p><dl><div><dt>注册</dt><dd>{{ instance.registrations?.enabled === true ? '开放注册' : instance.registrations?.enabled === false ? '已关闭' : '未提供状态' }}</dd></div><div><dt>网络</dt><dd>ActivityPub · Matrix</dd></div><div><dt>统计更新</dt><dd>{{ updated || '尚未更新' }}</dd></div></dl></template><router-link to="/admin/settings" class="admin-button">管理站点设置 <t-icon name="arrow-right" aria-hidden="true" /></router-link></section>
    </div>
    <section class="admin-card overview-federation"><div class="overview-federation-symbol" aria-hidden="true"><t-icon name="internet" :size="34" /></div><div><h2>让社区与更大的世界相连</h2><p>管理联邦中继、查看 Matrix 连接，或从原站点迁入完整数据。</p></div><div class="overview-federation-links"><router-link to="/admin/federation" class="admin-button">联邦中继</router-link><router-link to="/admin/system" class="admin-button">Matrix 服务</router-link></div></section>
    <AnalyticsOverview />
    <p class="overview-footnote">统计来自本站数据库；收录帖子包含已接收的远程内容。</p>
  </div>
</template>
<style scoped>
.overview-eyebrow{font-size:10px;font-weight:600;letter-spacing:2px;color:var(--color-label-secondary);margin-bottom:10px}.overview-metrics{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:16px}.overview-metric{padding:20px}.overview-metric-top{display:flex;align-items:center;justify-content:space-between;gap:8px;margin-bottom:0}.overview-metric-top h2{margin:0;font-size:13px;font-weight:500;color:var(--color-label-secondary)}.overview-metric-icon{display:grid;place-items:center;width:34px;height:34px;border-radius:10px;color:#2584df;background:#2584df14}.overview-metric-icon--purple{color:#8964dc;background:#8964dc14}.overview-metric-icon--green{color:#219a80;background:#219a8014}.overview-metric-icon--orange{color:#cc861f;background:#cc861f14}.overview-metric>strong{display:block;font-size:32px;line-height:1.2;font-weight:650;letter-spacing:-1px;font-variant-numeric:tabular-nums;overflow-wrap:anywhere}.overview-metric>p{font-size:11px;color:var(--color-label-secondary);margin:0}.overview-columns{display:grid;grid-template-columns:minmax(0,1.4fr) minmax(0,1fr);gap:20px}.overview-section-heading{display:flex;justify-content:space-between;align-items:center;gap:12px;margin-bottom:0}.overview-section-heading h2{margin:0}.overview-task{display:flex;align-items:center;gap:14px;padding:12px 0;border-bottom:1px solid var(--color-separator)}.overview-task:last-child{border-bottom:0;padding-bottom:0}.overview-task>span:nth-child(2){flex:1;min-width:0}.overview-task strong{display:block;font-size:14px;font-weight:550}.overview-task small{display:block;font-size:12px;margin-top:4px;color:var(--color-label-secondary)}.overview-task-icon{background:var(--color-bg-grouped);color:var(--admin-accent);border-radius:12px;width:44px;height:44px;display:grid;place-items:center;flex-shrink:0}@media(hover:hover) and (pointer:fine){.overview-task:hover strong{color:var(--admin-accent)}}.overview-instance-title{display:block;font-size:23px;margin:0}.overview-domain{font-size:13px;color:var(--color-label-secondary);overflow-wrap:anywhere}.overview-instance dl{display:block;font-size:13px;margin:0}.overview-instance dl div{display:flex;justify-content:space-between;gap:12px;padding:7px 0}.overview-instance dt{color:var(--color-label-secondary)}.overview-instance dd{margin:0;text-align:right}.overview-instance>.admin-button{width:100%;font-size:13px}.overview-federation{display:flex;align-items:center;gap:20px}.overview-federation-symbol{color:var(--admin-accent);display:grid;place-items:center;width:64px;height:64px;flex-shrink:0;background:color-mix(in srgb,var(--admin-accent) 8%,transparent);border-radius:18px}.overview-federation h2{margin-bottom:8px;font-size:16px}.overview-federation p{font-size:12px;color:var(--color-label-secondary);margin:0}.overview-federation-links{margin-left:auto;display:flex;gap:8px;flex-shrink:0;flex-wrap:wrap}.overview-federation-links a{font-size:12px}.overview-footnote{font-size:11px;color:var(--color-label-secondary);text-align:right}
@media(max-width:1150px){.overview-metrics{grid-template-columns:repeat(2,minmax(0,1fr))}.overview-federation{flex-wrap:wrap}.overview-federation-links{margin-left:84px}}@media(max-width:900px){.overview-columns{grid-template-columns:1fr}}@media(max-width:760px){.overview-metrics{gap:12px}.overview-metric{padding:16px}.overview-metric>strong{font-size:28px}.overview-metric-top{gap:4px}.overview-metric-icon{width:28px;height:28px}.overview-federation-symbol{display:none}.overview-federation-links{margin-left:0}.overview-federation{gap:16px}.overview-columns{gap:18px}}@media(max-width:360px){.overview-metric-icon{display:none}}
</style>
