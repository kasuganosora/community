<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { analyticsAdminApi, type AdminAnalytics, type AnalyticsDay } from '../../infrastructure/analyticsAdminApi'
const data = ref<AdminAnalytics | null>(null), error = ref(''), loading = ref(false)
const days = ref<7 | 30>(30)
const metric = ref<'local_posts' | 'posting_accounts'>('local_posts')
const selected = ref<AnalyticsDay | null>(null)
const visible = computed(() => data.value?.daily.slice(-days.value) ?? [])
const maximum = computed(() => Math.max(1, ...visible.value.map(day => day[metric.value])))
const heatMax = computed(() => Math.max(1, ...(data.value?.daily.map(day => day.local_posts) ?? [])))
const labels: Record<string, string> = { local_accounts: '本地账户', remote_accounts: '远端账户', local_posts: '本地帖子', remote_posts: '远端帖子', instances: '已收录实例', local_emojis: '本地表情', remote_emojis: '远端表情' }
const points = computed(() => visible.value.map((day, i) => `${20 + i * 660 / Math.max(1, visible.value.length - 1)},${160 - day[metric.value] * 140 / maximum.value}`).join(' '))
async function load() {
  if (loading.value) return
  loading.value = true; error.value = ''; data.value = null; selected.value = null
  try { data.value = await analyticsAdminApi.get(days.value) }
  catch (cause) { error.value = cause instanceof Error ? cause.message : '分析数据加载失败' }
  finally { loading.value = false }
}
function percent(size: number, retained: number) { return size ? `${(100 * retained / size).toFixed(1)}%` : '—' }
onMounted(load)
</script>
<template>
  <section class="analytics" aria-label="社区数据分析" :aria-busy="loading">
    <header><div><h2>社区数据分析</h2><p>UTC 日期 · 来自本站已存储数据；不代表整个联邦网络。</p></div><button class="admin-button" :disabled="loading" @click="load">刷新分析</button></header>
    <p v-if="loading" role="status">正在加载真实统计…</p>
    <p v-else-if="error" role="alert" class="admin-error">{{ error }} <button class="admin-button" @click="load">重试分析</button></p>
    <template v-else-if="data">
      <dl class="analytics-totals"><div v-for="(label, key) in labels" :key="key"><dt>{{ label }}</dt><dd>{{ data.totals[key]?.toLocaleString() ?? '—' }}</dd></div></dl>
      <p class="analytics-note">在线人数及历史登录活跃：不可用（尚无历史在线记录）。下方活跃用户是每天至少发布一次帖子的本地账户，包含转发，不等于在线或登录人数。帖子统计排除已删除及定时待发内容，包含私信与已收录远端帖子；不展示正文。</p>
      <section class="admin-card analytics-trend">
        <header><h3>发帖与活跃趋势</h3><div class="analytics-controls"><label>时间范围 <select v-model="days"><option :value="7">过去 7 天</option><option :value="30">过去 30 天</option></select></label><label>指标 <select v-model="metric"><option value="local_posts">本地发帖量</option><option value="posting_accounts">本地发帖活跃账户</option></select></label></div></header>
        <svg viewBox="0 0 700 190" role="img" :aria-label="`${days} 天${metric === 'local_posts' ? '本地发帖量' : '发帖活跃账户'}趋势`">
          <title>每日趋势，精确数字见下方数据表</title><path d="M20 20V160H680" class="axis" /><polyline :points="points" class="trend" />
          <circle v-for="(day, i) in visible" :key="day.date" :cx="20 + i * 660 / Math.max(1, visible.length - 1)" :cy="160 - day[metric] * 140 / maximum" r="4" tabindex="0" @focus="selected = day" @mouseenter="selected = day"><title>{{ day.date }}：{{ day[metric] }}</title></circle>
          <text x="20" y="185">{{ visible[0]?.date }}</text><text x="680" y="185" text-anchor="end">{{ visible.at(-1)?.date }}</text>
        </svg>
        <p aria-live="polite">{{ selected ? `${selected.date} · 本地帖子 ${selected.local_posts} · 远端帖子 ${selected.remote_posts} · 本地发帖账户 ${selected.posting_accounts}` : '悬停或聚焦圆点查看精确数字，也可展开数据表。' }}</p>
        <details><summary>查看趋势数据表</summary><div class="analytics-table"><table><caption>过去 {{ days }} 天（UTC），当天数据尚未完整</caption><thead><tr><th>日期</th><th>本地帖子</th><th>远端帖子</th><th>本地发帖账户</th></tr></thead><tbody><tr v-for="day in visible" :key="day.date"><th>{{ day.date }}</th><td>{{ day.local_posts }}</td><td>{{ day.remote_posts }}</td><td>{{ day.posting_accounts }}</td></tr></tbody></table></div></details>
      </section>
      <section class="admin-card analytics-heat"><h3>180 天本地发帖热力图</h3><p>颜色越深，发帖越多；零值表示该日未找到仍保留的帖子。</p><svg viewBox="0 0 540 148" role="img" aria-label="180 天每日本地发帖热力图"><title>从左至右每列七天，精确数字见数据表</title><rect v-for="(day, i) in data.daily" :key="day.date" :x="Math.floor(i / 7) * 20" :y="i % 7 * 20" width="16" height="16" rx="3" :opacity="day.local_posts ? 0.25 + 0.75 * day.local_posts / heatMax : 0.09"><title>{{ day.date }}：{{ day.local_posts }} 条本地帖子</title></rect></svg><details><summary>查看 180 天数据表</summary><div class="analytics-table"><table><thead><tr><th>日期</th><th>本地帖子</th></tr></thead><tbody><tr v-for="day in data.daily" :key="day.date"><th>{{ day.date }}</th><td>{{ day.local_posts }}</td></tr></tbody></table></div></details></section>
      <section class="admin-card analytics-retention"><h3>注册周队列留存</h3><p>最近 12 个已完成观察的 UTC 周一注册周；留存指注册周结束后的 7 天内至少发布一次帖子。不以当前最后发帖时间或登录状态代替历史行为。</p><div class="analytics-table"><table><thead><tr><th>注册周开始</th><th>本地注册账户</th><th>下一周发帖账户</th><th>留存率</th></tr></thead><tbody><tr v-for="cohort in data.cohorts" :key="cohort.date"><th>{{ cohort.date }}</th><td>{{ cohort.size }}</td><td>{{ cohort.retained }}</td><td>{{ percent(cohort.size, cohort.retained) }}</td></tr></tbody></table></div></section>
      <section class="admin-card analytics-federation"><h3>联邦关注分布</h3><p>已接受的跨站关注关系，按关系数排列前 20 个域名；不含待审核请求或已删除账户。</p><div class="analytics-distributions"><div v-for="direction in (['outgoing', 'incoming'] as const)" :key="direction"><h4>{{ direction === 'outgoing' ? '本站关注远端' : '远端关注本站' }}</h4><p v-if="!data.federation[direction].length">暂无已接受的跨站关注关系</p><table v-else><thead><tr><th>域名</th><th>关系数</th></tr></thead><tbody><tr v-for="row in data.federation[direction]" :key="row.domain"><th>{{ row.domain }}</th><td>{{ row.count }}</td></tr></tbody></table></div></div></section>
      <p class="analytics-note">更新时间：{{ new Date(data.generated_at).toLocaleString() }}。历史曲线按仍保存的帖子重新计算，删除内容会影响过去日期；资料总量包含已收录账户，并非人类用户去重人数。</p>
    </template>
  </section>
</template>
<style scoped>
.analytics{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:20px}.analytics>*{min-width:0;grid-column:1/-1}.analytics-heat,.analytics-retention{grid-column:auto}.analytics-heat{align-self:start}.analytics header{display:flex;justify-content:space-between;align-items:center;gap:16px;flex-wrap:wrap}.analytics h2,.analytics h3{margin:0}.analytics p{font-size:13px;color:var(--color-label-secondary);line-height:1.6}.analytics-totals{display:grid;grid-template-columns:repeat(auto-fit,minmax(120px,1fr));gap:12px;margin:0}.analytics-totals>div{padding:16px;border:1px solid var(--color-separator);border-radius:12px}.analytics dt{font-size:12px}.analytics dd{font-size:24px;margin:8px 0 0;font-variant-numeric:tabular-nums}.analytics-controls{display:flex;gap:12px;flex-wrap:wrap}.analytics label{font-size:13px;display:grid;gap:6px;min-width:0}.analytics-controls{align-items:end}.analytics-totals>div{background:var(--color-bg-row)}.analytics select{padding:6px;color:inherit;background:var(--color-bg-grouped);border:1px solid var(--color-separator);border-radius:6px}.analytics svg{width:100%;max-height:270px;overflow:visible}.analytics svg rect,.analytics svg circle{fill:var(--admin-accent,#397dda)}.analytics svg circle:focus{outline:2px solid currentColor}.analytics svg text{font-size:11px;fill:currentColor}.axis{fill:none;stroke:var(--color-separator)}.trend{fill:none;stroke:var(--admin-accent,#397dda);stroke-width:2}.analytics-table{overflow:auto;max-height:360px}.analytics table{width:100%;border-collapse:collapse;font-size:13px}.analytics th,.analytics td{text-align:left;padding:9px;border-bottom:1px solid var(--color-separator);overflow-wrap:anywhere}.analytics td{font-variant-numeric:tabular-nums}.analytics summary{cursor:pointer;padding:10px 0}.analytics-distributions{display:grid;grid-template-columns:1fr 1fr;gap:24px}@media(max-width:1100px){.analytics{grid-template-columns:1fr}}@media(max-width:700px){.analytics-distributions{grid-template-columns:1fr}.analytics-totals{grid-template-columns:repeat(2,minmax(0,1fr))}}
</style>
