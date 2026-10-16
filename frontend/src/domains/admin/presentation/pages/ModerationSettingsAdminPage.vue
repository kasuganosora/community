<script setup lang="ts">
import { isAxiosError } from 'axios'
import { computed, onMounted, reactive, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { moderationSettingsApi as api, invalidRegexLine, splitLines, type ModerationSettings, type ModerationUpdate, type UgcVisibility } from '../../infrastructure/moderationSettingsApi'
import { operationsAdminApi, type SiteRule } from '../../infrastructure/operationsAdminApi'
import { AdminAlert, AdminButton, AdminCheck, AdminField, AdminPageHeader } from '../components/ui'

type ListKey = 'preserved_usernames' | 'sensitive_words' | 'prohibited_words' | 'prohibited_words_for_name_of_user' | 'hidden_tags' | 'silenced_hosts' | 'media_silenced_hosts' | 'blocked_hosts'
interface ListSection { key: ListKey; title: string; icon: string; hint: string[]; words?: boolean; placeholder: string }

const LISTS: ListSection[] = [
  { key: 'preserved_usernames', title: '保留的用户名', icon: '🔒', placeholder: 'admin\nsupport', hint: ['被保留的用户名无法用于注册，每行一个，不区分大小写。管理员手动创建账号不受此限制。'] },
  { key: 'sensitive_words', title: '敏感词', icon: '⚠️', words: true, placeholder: 'word\nword1 word2\n/re(gex)?/i', hint: ['包含敏感词的公开帖子仍会发布，但可见性会被改为“不公开”（不出现在公共时间线）。有内容警告时检查警告文字，否则检查正文。', '每行一条：用空格分隔表示同时包含（AND）；用斜杠包围表示正则表达式，如 /foo(bar)?/i。'] },
  { key: 'prohibited_words', title: '禁止词', icon: '⛔', words: true, placeholder: 'word\nword1 word2\n/re(gex)?/i', hint: ['包含禁止词的帖子无法发布（检查内容警告、正文和投票选项），远端实例送达的此类帖子也会被拒收。', '每行一条：用空格分隔表示同时包含（AND）；用斜杠包围表示正则表达式，如 /foo(bar)?/i。'] },
  { key: 'prohibited_words_for_name_of_user', title: '用户名与昵称禁止词', icon: '🚫', words: true, placeholder: 'word\n/re(gex)?/', hint: ['包含这些词的用户名或显示昵称无法用于注册或修改资料，已有账号不受影响。', '每行一条：用空格分隔表示同时包含（AND）；用斜杠包围表示正则表达式。'] },
  { key: 'hidden_tags', title: '隐藏标签', icon: '#', placeholder: 'tag', hint: ['这些话题标签不会出现在热门话题和标签建议中，每行一个，可省略 #。'] },
  { key: 'silenced_hosts', title: '静音的实例', icon: '🔇', placeholder: 'example.com', hint: ['每行一个主机名，子域名一并生效。来自这些实例的公开帖子会改为“不公开”，不出现在公共/联邦时间线；关注仍然有效，但对方关注本站用户时总是需要批准。'] },
  { key: 'media_silenced_hosts', title: '媒体静音的实例', icon: '🖼️', placeholder: 'example.com', hint: ['每行一个主机名（仅精确匹配）。来自这些实例的媒体文件会被标记为敏感内容。'] },
  { key: 'blocked_hosts', title: '屏蔽的实例', icon: '🛑', placeholder: 'example.com', hint: ['每行一个主机名，子域名一并屏蔽。与这些实例之间的所有联邦通信（收信、投递和抓取）都会停止，其已有内容与账号对本站隐藏。'] },
]
const UGC_OPTIONS: { value: UgcVisibility; label: string }[] = [
  { value: 'all', label: '全部公开' },
  { value: 'local', label: '仅本站内容（隐藏远端内容）' },
  { value: 'none', label: '不公开' },
]

const data = ref<ModerationSettings>()
const drafts = reactive<Record<ListKey, string>>({ preserved_usernames: '', sensitive_words: '', prohibited_words: '', prohibited_words_for_name_of_user: '', hidden_tags: '', silenced_hosts: '', media_silenced_hosts: '', blocked_hosts: '' })
const fieldErrors = reactive<Partial<Record<ListKey, string>>>({})
const loading = ref(false), busy = ref<string>(''), error = ref(''), notice = ref('')
const rules = ref<SiteRule[]>([]), ruleDraft = ref(''), ruleEdits = reactive<Record<string, string>>({}), rulesError = ref('')

const locked = computed(() => new Set(data.value?.environment_locked || []))
function install(s: ModerationSettings) {
  data.value = s
  for (const l of LISTS) drafts[l.key] = (s[l.key] || []).join('\n')
}
function dirty(key: ListKey) { return data.value ? drafts[key] !== (data.value[key] || []).join('\n') : false }
function count(key: ListKey) { return data.value?.[key]?.length || 0 }
function fmt(v?: string) { return v ? new Date(v).toLocaleString() : '' }

async function load() {
  loading.value = true; error.value = ''
  try {
    const [s, r] = await Promise.all([api.get(), operationsAdminApi.rules().catch(() => [] as SiteRule[])])
    install(s); rules.value = r
  } catch { error.value = '无法读取管理设置，请重试。' }
  finally { loading.value = false }
}
async function save(update: ModerationUpdate, tag: string, ok = '已保存，新请求立即生效。') {
  busy.value = tag; error.value = ''; notice.value = ''
  try { install(await api.save(update)); notice.value = ok; return true }
  catch (cause) {
    const status = isAxiosError(cause) ? cause.response?.status : undefined
    const detail = isAxiosError(cause) ? cause.response?.data?.error : undefined
    error.value = status === 409 ? '此设置由部署环境锁定，无法在后台修改。' : status === 403 ? '当前账号没有修改管理设置的权限。' : `保存失败${detail ? `：${detail}` : ''}，输入已保留。`
    return false
  } finally { busy.value = '' }
}
async function toggleRegistration(value: boolean) {
  if (!data.value) return
  if (value && !window.confirm('开放注册后任何人都可以注册账号。请确认已准备好审核与应对垃圾注册，并了解若启用了自动关闭，管理员长时间不活跃时注册将被自动关闭。确定开放注册吗？')) return
  await save({ enable_registration: value }, 'registration')
}
async function saveList(l: ListSection) {
  const lines = splitLines(drafts[l.key])
  delete fieldErrors[l.key]
  if (l.words) {
    const bad = invalidRegexLine(lines)
    if (bad) { fieldErrors[l.key] = `正则表达式无效：${bad}`; return }
  }
  await save({ [l.key]: lines } as ModerationUpdate, l.key, `${l.title}已保存。`)
}

async function addRule() {
  const text = ruleDraft.value.trim()
  if (!text) return
  busy.value = 'rules'; rulesError.value = ''
  try { rules.value = [...rules.value, await operationsAdminApi.saveRule(null, text)]; ruleDraft.value = '' }
  catch { rulesError.value = '无法添加规则，请重试。' } finally { busy.value = '' }
}
async function updateRule(r: SiteRule) {
  const text = (ruleEdits[r.id] ?? r.text).trim()
  if (!text || text === r.text) { delete ruleEdits[r.id]; return }
  busy.value = 'rules'; rulesError.value = ''
  try { const saved = await operationsAdminApi.saveRule(r.id, text); rules.value = rules.value.map(x => x.id === r.id ? saved : x); delete ruleEdits[r.id] }
  catch { rulesError.value = '无法保存规则，请重试。' } finally { busy.value = '' }
}
async function removeRule(r: SiteRule) {
  if (!window.confirm(`删除规则“${r.text}”？`)) return
  busy.value = 'rules'; rulesError.value = ''
  try { await operationsAdminApi.deleteRule(r.id); rules.value = rules.value.filter(x => x.id !== r.id) }
  catch { rulesError.value = '无法删除规则，请重试。' } finally { busy.value = '' }
}
onMounted(load)
</script>

<template>
  <div class="admin-page moderation-settings">
    <AdminPageHeader title="管理" description="注册、访客可见性、内容关键词与联邦实例的审核设置（对应 Misskey 的“管理”页）。保存后新请求立即生效。" />
    <p v-if="loading" class="admin-empty" role="status">正在读取设置…</p>
    <AdminAlert v-if="error" tone="danger">{{ error }}<template v-if="!data" #action><AdminButton size="sm" :disabled="loading" @click="load">重试</AdminButton></template></AdminAlert>
    <AdminAlert v-if="notice" tone="success">{{ notice }}</AdminAlert>

    <template v-if="data">
      <section class="admin-card admin-form mod-basic" aria-label="基本设置">
        <AdminCheck variant="switch" :model-value="data.enable_registration" label="开放注册"
          :hint="data.auto_close_registration ? `关闭后仅能通过邀请码注册（取决于注册管理设置）。若所有管理员连续 ${data.moderator_inactivity_limit_days} 天没有活动，此设置将自动关闭。` : '关闭后仅能通过邀请码注册（取决于注册管理设置）。'"
          :disabled="!!busy || locked.has('enable_registration')" @update:model-value="toggleRegistration" />
        <div class="mod-sub" data-testid="auto-close-registration">
          <AdminCheck variant="switch" :model-value="data.auto_close_registration" label="管理员不活跃时自动关闭注册"
            :hint="(data.auto_close_registration ? `开启时，若所有管理员连续 ${data.moderator_inactivity_limit_days} 天没有活动，开放注册将被自动关闭。` : '已关闭：无论管理员是否活跃，都不会自动关闭注册。') + (data.moderators_last_active_at ? ` 管理员最近活动：${fmt(data.moderators_last_active_at)}` : ' 尚无管理员活动记录。')"
            :disabled="!!busy" @update:model-value="v => save({ auto_close_registration: v }, 'auto')" />
        </div>
        <p v-if="data.enable_registration" class="mod-warn">⚠ 开放注册存在被滥用的风险，建议同时启用邮箱验证、验证码，并保持有人审核。</p>
        <p v-if="data.registration_auto_closed_at && !data.enable_registration" class="mod-warn">注册已于 {{ fmt(data.registration_auto_closed_at) }} 因管理员长时间不活跃而自动关闭。</p>
        <AdminCheck variant="switch" :model-value="data.email_required_for_signup" label="注册时需要验证邮箱（推荐）"
          :disabled="!!busy || locked.has('email_required_for_signup')" @update:model-value="v => save({ email_required_for_signup: v }, 'email')" />
        <AdminField label="访客可见的用户内容" hint="控制未登录访客能看到的帖子与用户资料。“仅本站内容”会隐藏远端实例的帖子和账号；“不公开”时访客无法浏览任何用户内容，搜索引擎也会被要求不收录用户页与帖子页。">
          <template #default="{ id, describedBy }">
            <select :id="id" :aria-describedby="describedBy" :value="data.ugc_visibility_for_visitor" :disabled="!!busy"
              @change="save({ ugc_visibility_for_visitor: ($event.target as HTMLSelectElement).value as UgcVisibility }, 'ugc')">
              <option v-for="o in UGC_OPTIONS" :key="o.value" :value="o.value">{{ o.label }}</option>
            </select>
          </template>
        </AdminField>
      </section>

      <details class="admin-card mod-folder">
        <summary><span class="mod-folder__icon" aria-hidden="true">📜</span><span class="mod-folder__title">服务器规则</span><span class="mod-folder__count">{{ rules.length }}</span></summary>
        <div class="mod-folder__body">
          <p class="mod-hint">规则会显示在注册页和关于页。完整排序与管理也可在 <RouterLink to="/admin/rules">社区规则</RouterLink> 页面进行。</p>
          <ol class="mod-rules">
            <li v-for="r in rules" :key="r.id">
              <input :value="ruleEdits[r.id] ?? r.text" maxlength="255" :aria-label="`规则 ${r.text}`" :disabled="busy === 'rules'" @input="ruleEdits[r.id] = ($event.target as HTMLInputElement).value" @keydown.enter.prevent="updateRule(r)" />
              <AdminButton size="sm" :disabled="busy === 'rules' || ruleEdits[r.id] === undefined" @click="updateRule(r)">保存</AdminButton>
              <AdminButton size="sm" variant="danger" :disabled="busy === 'rules'" @click="removeRule(r)">删除</AdminButton>
            </li>
          </ol>
          <form class="mod-rules__add" @submit.prevent="addRule">
            <input v-model="ruleDraft" maxlength="255" placeholder="新规则" aria-label="新规则" :disabled="busy === 'rules'" />
            <AdminButton type="submit" variant="primary" size="sm" :disabled="busy === 'rules' || !ruleDraft.trim()">添加</AdminButton>
          </form>
          <p v-if="rulesError" class="admin-field__error" role="alert">{{ rulesError }}</p>
        </div>
      </details>

      <details v-for="l in LISTS" :key="l.key" class="admin-card mod-folder" :data-key="l.key">
        <summary><span class="mod-folder__icon" aria-hidden="true">{{ l.icon }}</span><span class="mod-folder__title">{{ l.title }}</span><span class="mod-folder__count">{{ count(l.key) }}</span></summary>
        <form class="mod-folder__body" @submit.prevent="saveList(l)">
          <AdminField :label="l.title" :error="fieldErrors[l.key]" :hint="l.hint.join(' ')">
            <template #default="{ id, describedBy }">
              <textarea :id="id" v-model="drafts[l.key]" :aria-describedby="describedBy" :aria-invalid="fieldErrors[l.key] ? true : undefined" rows="8" spellcheck="false" autocapitalize="off" autocomplete="off" :placeholder="l.placeholder" :disabled="busy === l.key" />
            </template>
          </AdminField>
          <div class="admin-form-actions">
            <AdminButton type="submit" variant="primary" :loading="busy === l.key" :disabled="!!busy || !dirty(l.key)">保存</AdminButton>
            <AdminButton :disabled="!!busy || !dirty(l.key)" @click="drafts[l.key] = (data[l.key] || []).join('\n')">还原</AdminButton>
          </div>
        </form>
      </details>
    </template>
  </div>
</template>

<style scoped>
.moderation-settings { display: flex; flex-direction: column; gap: 12px; max-width: 760px; }
.moderation-settings > .admin-card { max-width: none; width: 100%; box-sizing: border-box; }
.mod-basic { display: flex; flex-direction: column; gap: 14px; }
.mod-basic select { width: 100%; }
.mod-warn { margin: -6px 0 0; font-size: 12px; line-height: 1.6; color: var(--color-warning, #b7791f); }
.mod-hint { margin: 0 0 10px; font-size: 13px; line-height: 1.6; color: var(--color-label-secondary); }
.mod-folder { padding: 0; overflow: hidden; }
.mod-folder > summary { display: flex; align-items: center; gap: 10px; min-height: 48px; padding: 0 16px; cursor: pointer; list-style: none; user-select: none; }
.mod-folder > summary::-webkit-details-marker { display: none; }
.mod-folder > summary::after { content: ''; width: 8px; height: 8px; margin-left: 4px; border-right: 2px solid var(--color-label-tertiary); border-bottom: 2px solid var(--color-label-tertiary); transform: rotate(-45deg); transition: transform .15s; }
.mod-folder[open] > summary::after { transform: rotate(45deg); }
.mod-folder > summary:focus-visible { outline: 2px solid var(--admin-accent); outline-offset: -2px; }
.mod-folder__icon { width: 22px; text-align: center; }
.mod-folder__title { flex: 1; font-weight: 600; }
.mod-folder__count { font-size: 12px; min-width: 22px; text-align: center; padding: 1px 8px; border-radius: 999px; background: var(--color-fill); color: var(--color-label-secondary); font-variant-numeric: tabular-nums; }
.mod-folder__body { padding: 4px 16px 16px; border-top: 1px solid var(--color-separator); display: flex; flex-direction: column; gap: 10px; }
.mod-folder__body textarea { width: 100%; box-sizing: border-box; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 13px; min-height: 140px; resize: vertical; }
.mod-rules { margin: 0; padding-left: 22px; display: flex; flex-direction: column; gap: 8px; }
.mod-rules li { display: flex; gap: 6px; align-items: center; }
.mod-rules input, .mod-rules__add input { flex: 1; min-width: 0; height: 34px; box-sizing: border-box; padding: 0 10px; border-radius: 8px; border: 1px solid var(--color-separator); background: var(--admin-control-bg); color: var(--color-label); font: inherit; }
.mod-rules__add { display: flex; gap: 6px; }
@media (max-width: 640px) {
  .mod-rules li { flex-wrap: wrap; }
  .mod-rules li input { flex-basis: 100%; }
}
.mod-sub { margin-left: 28px; padding-left: 12px; border-left: 2px solid var(--td-component-stroke, #e5e7eb); }
</style>
