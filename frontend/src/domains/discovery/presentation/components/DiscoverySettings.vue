<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref, watch } from 'vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { discoveryApi, type DiscoveryFeedback, type ReadingPreference } from '../../infrastructure/discoveryApi'
import { invalidateDiscovery, loadDiscoveryPreference } from '../composables/discoveryState'

const preference = ref<ReadingPreference>('generic'), feedback = ref<DiscoveryFeedback[]>([])
const languages = ref<string[]>([]), languageInput = ref(''), languageError = ref('')
const loading = ref(true), ready = ref(false), enabled = ref(true), busy = ref(false), error = ref(''), saved = ref('')
let version = 0
const labels = { hide_status: '不感兴趣的帖子', hide_author: '停止推荐的作者', hide_tag: '停止推荐的话题', dismiss_suggestion: '移除的关注建议' }
const commonLanguages = [
  ['zh', '中文'], ['zh-Hans', '简体中文'], ['zh-Hant', '繁體中文'], ['en', 'English'], ['ja', '日本語'],
  ['ko', '한국어'], ['fr', 'Français'], ['de', 'Deutsch'], ['es', 'Español'], ['pt', 'Português'], ['ru', 'Русский'], ['ar', 'العربية'],
] as const
function languageName(code: string) { return commonLanguages.find(([value]) => value === code)?.[1] || code }
function addLanguage(code = languageInput.value) {
  languageError.value = ''
  const value = code.trim()
  if (!value) return
  if (languages.value.length >= 4) { languageError.value = '最多选择 4 种阅读语言。'; return }
  try {
    const canonical = Intl.getCanonicalLocales(value)[0]
    if (!canonical) throw new Error('invalid language')
    if (!languages.value.includes(canonical)) languages.value = [...languages.value, canonical]
    languageInput.value = ''
  } catch { languageError.value = '请输入标准语言代码，例如 zh、en 或 zh-Hant。' }
}
function selectLanguage(event: Event) {
  const select = event.target as HTMLSelectElement
  addLanguage(select.value); select.value = ''
}
async function load() {
  const mine = ++version, epoch = sessionEpoch.value
  loading.value = true; ready.value = false; error.value = ''; feedback.value = []
  try {
    const [settings, rows] = await Promise.all([loadDiscoveryPreference(true), discoveryApi.feedback()])
    if (mine !== version || epoch !== sessionEpoch.value || !settings) return
    preference.value = settings.preference; languages.value = settings.languages || []; languageError.value = ''; languageInput.value = ''; enabled.value = settings.enabled !== false; feedback.value = rows; ready.value = true
  } catch (e) { if (mine === version) error.value = e instanceof Error ? e.message : '无法读取发现设置' }
  finally { if (mine === version) loading.value = false }
}
async function save() {
  if (busy.value || !ready.value) return
  languageError.value = ''
  if (languageInput.value.trim()) addLanguage()
  if (languageError.value) return
  busy.value = true; error.value = ''; saved.value = ''
  const mine = version, epoch = sessionEpoch.value
  try {
    const value = await discoveryApi.setPreference(preference.value, languages.value)
    if (mine !== version || epoch !== sessionEpoch.value) return
    preference.value = value.preference; invalidateDiscovery(value); saved.value = '发现偏好已保存。'
  } catch (e) { if (mine === version) error.value = e instanceof Error ? e.message : '保存失败' }
  finally { if (mine === version) busy.value = false }
}
async function undo(item: DiscoveryFeedback) {
  if (busy.value) return
  busy.value = true; error.value = ''; saved.value = ''
  const mine = version, epoch = sessionEpoch.value
  try {
    const value = await discoveryApi.removeFeedback(item.id)
    if (mine !== version || epoch !== sessionEpoch.value) return
    feedback.value = feedback.value.filter(row => row.id !== item.id)
    invalidateDiscovery(value); saved.value = '已撤销这条发现规则。'
  } catch (e) { if (mine === version) error.value = e instanceof Error ? e.message : '撤销失败' }
  finally { if (mine === version) busy.value = false }
}
watch(sessionEpoch, () => { busy.value = false; saved.value = ''; void load() }, { flush: 'sync' })
onMounted(load)
onBeforeUnmount(() => { ++version })
</script>

<template>
  <div class="discovery-settings" :aria-busy="loading || busy">
    <p v-if="loading" role="status">正在读取发现偏好…</p>
    <form v-else class="settings-form" @submit.prevent="save">
      <p v-if="!enabled" class="hint">本站尚未开放帖子与账号发现。你可以预先保存偏好，开放后生效。</p>
      <fieldset :disabled="busy || !ready" class="discovery-options">
        <legend class="settings-field__label">首页与关注建议</legend>
        <label class="settings-check"><input v-model="preference" type="radio" value="off" name="discovery-mode" /><span><strong>关闭发现</strong><small>首页打开关注时间线，不显示推荐帖子与关注建议。</small></span></label>
        <label class="settings-check"><input v-model="preference" type="radio" value="generic" name="discovery-mode" /><span><strong>公开发现</strong><small>按近期公开内容与本站观察到的热度发现，不使用个人互动排序。</small></span></label>
        <label class="settings-check"><input v-model="preference" type="radio" value="personalized" name="discovery-mode" /><span><strong>个性化推荐</strong><small>使用关注的话题和公开互动，发现可能感兴趣的内容。私信与群聊不参与。</small></span></label>
      </fieldset>
      <fieldset :disabled="busy || !ready" class="discovery-options" aria-describedby="discovery-language-hint">
        <legend class="settings-field__label">阅读语言</legend>
        <p id="discovery-language-hint" class="hint">留空表示不限语言，最多选择 4 种。未标注语言的帖子仍可能出现；这不会改变发帖时的默认语言。</p>
        <div class="discovery-languages">
          <label class="settings-field"><span class="settings-field__label">常用语言</span><select class="settings-input" aria-label="添加常用阅读语言" :disabled="languages.length >= 4" @change="selectLanguage"><option value="">选择语言…</option><option v-for="[code, name] in commonLanguages" :key="code" :value="code" :disabled="languages.includes(code)">{{ name }}</option></select></label>
          <label class="settings-field"><span class="settings-field__label">其他语言代码</span><span class="discovery-language-input"><input v-model="languageInput" class="settings-input" placeholder="例如 it、nl、uk" maxlength="35" :disabled="languages.length >= 4" @keydown.enter.prevent="addLanguage()" /><button type="button" class="btn btn--secondary btn--sm" :disabled="!languageInput.trim() || languages.length >= 4" @click="addLanguage()">添加</button></span></label>
        </div>
        <ul v-if="languages.length" class="discovery-language-values"><li v-for="code in languages" :key="code"><button type="button" class="btn btn--secondary btn--sm" :aria-label="`移除阅读语言 ${languageName(code)}`" @click="languages = languages.filter(item => item !== code); languageError = ''">{{ languageName(code) }} <span aria-hidden="true">×</span></button></li></ul>
        <p v-if="languageError" class="settings-error" role="alert">{{ languageError }}</p>
      </fieldset>
      <p class="hint">热点话题独立于此设置。关闭发现不会删除以下手动规则，也不会改变是否允许推荐你的账号。</p>
      <div class="settings-actions"><button type="submit" class="btn btn--primary btn--sm" :disabled="busy || !ready">{{ busy ? '保存中…' : '保存发现偏好' }}</button></div>
    </form>
    <p v-if="error" class="settings-error" role="alert">{{ error }} <button type="button" class="btn btn--ghost btn--sm" :disabled="busy" @click="load">重新读取</button></p>
    <p v-if="saved" class="hint" role="status">{{ saved }}</p>
    <section v-if="!loading" class="discovery-rules" aria-labelledby="discovery-rules-title">
      <h3 id="discovery-rules-title">管理发现规则</h3>
      <p class="hint">这些规则只影响推荐与热点，不等于屏蔽账号。移除关注建议与停止推荐作者可分别撤销。</p>
      <p v-if="!feedback.length && !error" class="hint">还没有手动添加的规则。</p>
      <ul v-else>
        <li v-for="item in feedback" :key="item.id"><span><strong>{{ labels[item.action] || '发现规则' }}</strong><small>{{ item.target_label || `${item.target_kind === 'tag' ? '#' : item.target_kind === 'account' ? '账号 ID ' : '帖子 ID '}${item.target_key}` }}</small><small v-if="item.created_at">{{ new Date(item.created_at).toLocaleDateString() }}</small></span><button type="button" class="btn btn--secondary btn--sm" :disabled="busy" @click="undo(item)">撤销</button></li>
      </ul>
    </section>
  </div>
</template>

<style scoped>
.discovery-settings { display: flex; flex-direction: column; gap: 1rem; }
.discovery-options { border: 0; margin: 0; padding: 0; display: grid; gap: .75rem; }
.discovery-options legend { margin-bottom: .75rem; }
.discovery-options .settings-check { align-items: flex-start; }
.discovery-options input { margin-top: .25rem; }
.discovery-options strong, .discovery-options small, .discovery-rules strong, .discovery-rules small { display: block; }
.discovery-options small, .discovery-rules small { color: var(--color-label-secondary); line-height: 1.6; font-size: .8125rem; margin-top: .25rem; overflow-wrap: anywhere; }
.discovery-rules { border-top: 1px solid var(--color-separator); padding-top: 1rem; }
.discovery-rules h3 { font-size: 1rem; margin-bottom: .5rem; }
.discovery-rules ul { list-style: none; padding: 0; }
.discovery-rules li { display: flex; align-items: center; justify-content: space-between; gap: 1rem; padding: .75rem 0; border-bottom: 1px solid var(--color-separator); }
.discovery-rules li > span { min-width: 0; }
.discovery-rules button { flex-shrink: 0; }
.discovery-languages { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: .75rem; }
.discovery-language-input { display: flex; align-items: center; gap: .5rem; min-width: 0; }
.discovery-language-input input { min-width: 0; }
.discovery-language-input button { flex: none; }
.discovery-language-values { display: flex; flex-wrap: wrap; gap: .5rem; list-style: none; padding: 0; }
@media (max-width: 699px) { .discovery-languages { grid-template-columns: minmax(0, 1fr); } }
</style>
