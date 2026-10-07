<script setup lang="ts">
import { computed, onUnmounted, reactive, ref, watch } from 'vue'
import { settingsAdminApi as api, type SettingsSection } from '../../infrastructure/settingsAdminApi'
import { tokenService } from '@/shared/api/client'
import SettingsAdminPage from './SettingsAdminPage.vue'

const props = defineProps<{ section: string }>()
const data = ref<SettingsSection>()
const draft = reactive<Record<string, string | number | boolean>>({})
const clearSecrets = reactive<Record<string, boolean>>({})
const loading = ref(false)
const busy = ref(false)
const testing = ref(false)
const error = ref('')
const notice = ref('')
const testNotice = ref('')
let generation = 0

const editable = computed(() => data.value?.fields.some(field => !field.read_only))
const changes = computed(() => {
  const values: Record<string, string | number | boolean> = {}
  const clear: string[] = []
  for (const field of data.value?.fields || []) {
    if (field.read_only) continue
    if (field.type === 'secret') {
      if (clearSecrets[field.key]) clear.push(field.key)
      else if (draft[field.key]) values[field.key] = draft[field.key]!
    } else if (draft[field.key] !== field.value) values[field.key] = draft[field.key]!
  }
  return { values, clear }
})
const dirty = computed(() => Object.keys(changes.value.values).length + changes.value.clear.length > 0)
const connectionSection = computed(() => props.section === 'email' || props.section === 'storage')
function install(section: SettingsSection) {
  data.value = section
  for (const key of Object.keys(draft)) delete draft[key]
  for (const key of Object.keys(clearSecrets)) delete clearSecrets[key]
  for (const field of section.fields) draft[field.key] = field.type === 'secret' ? '' : field.value ?? ''
}
async function load() {
  const version = ++generation
  const identity = tokenService.get()
  data.value = undefined
  for (const key of Object.keys(draft)) delete draft[key]
  for (const key of Object.keys(clearSecrets)) delete clearSecrets[key]
  error.value = ''; notice.value = ''; testNotice.value = ''
  busy.value = false; testing.value = false
  if (props.section === 'security') return
  loading.value = true
  try {
    const result = await api.get(props.section)
    if (version === generation && identity === tokenService.get()) install(result)
  } catch {
    if (version === generation) error.value = '无法读取设置，请重试。'
  } finally {
    if (version === generation) loading.value = false
  }
}
async function save() {
  if (busy.value || !dirty.value || !data.value) return
  if (changes.value.clear.length && !window.confirm('确认清除选中的已保存密钥？相关服务可能无法连接，保存后需要重新配置。')) return
  const version = generation
  const identity = tokenService.get()
  const restart = data.value.fields.some(f => f.restart_required && (f.key in changes.value.values || changes.value.clear.includes(f.key)))
  busy.value = true; error.value = ''; notice.value = ''; testNotice.value = ''
  try {
    const result = await api.save(props.section, changes.value.values, changes.value.clear)
    if (version !== generation || identity !== tokenService.get()) return
    install(result)
    notice.value = restart ? '配置已保存。请由部署管理员重启服务，使新的连接配置生效。' : '设置已保存，新请求立即生效。'
  } catch {
    if (version === generation) error.value = '保存失败，输入已保留。请检查字段内容、范围或部署配置覆盖后重试。'
  } finally {
    if (version === generation) busy.value = false
  }
}
async function testConnection() {
  if (testing.value || busy.value || dirty.value || (props.section !== 'email' && props.section !== 'storage')) return
  const version = generation
  testing.value = true; testNotice.value = ''; error.value = ''
  try {
    const result = await api.test(props.section)
    if (version !== generation) return
    if (result.success) testNotice.value = result.message || '连接测试成功。'
    else error.value = result.message || '连接测试失败，请检查已保存配置。'
  } catch {
    if (version === generation) error.value = '连接测试失败，请检查已保存配置与网络限制后重试。'
  } finally {
    if (version === generation) testing.value = false
  }
}
watch(() => props.section, load, { immediate: true })
onUnmounted(() => { generation++; for (const key of Object.keys(draft)) delete draft[key] })
</script>
<template>
  <SettingsAdminPage v-if="section === 'security'" mode="security" />
  <div v-else class="admin-page">
    <header><h1>{{ data?.title || '设置' }}</h1><p v-if="data">{{ data.description }}</p></header>
    <p v-if="loading" role="status">正在读取设置…</p>
    <p v-if="error" class="admin-error" role="alert">{{ error }} <button v-if="!data" class="admin-button" :disabled="loading" @click="load">重试</button></p>
    <p v-if="notice" role="status">{{ notice }}</p>
    <form v-if="data" class="admin-card settings-center" @submit.prevent="save">
      <div v-for="field in data.fields" :key="field.key" class="settings-center__field">
        <label class="admin-field" :for="`setting-${field.key}`">{{ field.label }}</label>
        <template v-if="field.read_only">
          <p v-if="field.type === 'secret'">{{ field.secret_set ? '已配置密钥' : '未配置密钥' }}</p>
          <p v-else class="settings-center__value">{{ typeof field.value === 'boolean' ? (field.value ? '开启' : '关闭') : field.value ?? '未配置' }}</p>
          <small>{{ field.source === 'environment' ? '由部署环境管理，请修改服务器配置。' : '当前运行状态，只读。' }}</small>
        </template>
        <template v-else>
          <select v-if="field.options?.length" :id="`setting-${field.key}`" v-model="draft[field.key]" :disabled="busy || testing"><option v-for="option in field.options" :key="option" :value="option">{{ option }}</option></select>
          <textarea v-else-if="field.type === 'textarea'" :id="`setting-${field.key}`" :value="String(draft[field.key] ?? '')" @input="draft[field.key] = ($event.target as HTMLTextAreaElement).value" :maxlength="field.max" :disabled="busy || testing" rows="5" />
          <input v-else-if="field.type === 'boolean'" :id="`setting-${field.key}`" v-model="draft[field.key]" type="checkbox" :disabled="busy || testing" />
          <input v-else-if="field.type === 'number'" :id="`setting-${field.key}`" v-model.number="draft[field.key]" type="number" required :min="field.min" :max="field.max" :disabled="busy || testing" />
          <template v-else-if="field.type === 'secret'">
            <input :id="`setting-${field.key}`" v-model="draft[field.key]" type="password" autocomplete="new-password" placeholder="留空保留现有密钥" :disabled="busy || testing || clearSecrets[field.key]" />
            <small>{{ field.secret_set ? '已保存密钥，不回显。' : '尚未保存密钥。' }}</small>
            <label v-if="field.secret_set" class="settings-center__clear"><input v-model="clearSecrets[field.key]" type="checkbox" :disabled="busy || testing" />明确清除已保存的{{ field.label }}</label>
          </template>
          <input v-else :id="`setting-${field.key}`" v-model="draft[field.key]" :type="field.type" :maxlength="field.max" :disabled="busy || testing" />
          <small v-if="field.restart_required">保存后需要重启服务。</small>
        </template>
        <p v-if="field.description">{{ field.description }}</p>
      </div>
      <div class="admin-toolbar">
        <button v-if="editable" class="admin-button admin-button--primary" :disabled="busy || testing || !dirty">{{ busy ? '保存中…' : '保存设置' }}</button>
        <button class="admin-button" type="button" :disabled="busy || testing || dirty" @click="load">刷新</button>
      </div>
    </form>
    <section v-if="connectionSection && data" class="admin-card">
      <h2>连接测试</h2>
      <p>仅在点击后检查已保存的连接配置。请先保存表单；邮件测试不发送邮件，存储测试不上传或删除文件。</p>
      <button class="admin-button" :disabled="busy || testing || dirty" @click="testConnection">{{ testing ? '正在测试…' : '测试已保存配置' }}</button>
      <p v-if="testNotice" role="status">{{ testNotice }}</p>
    </section>
  </div>
</template>
<style scoped>
.settings-center { display: grid; gap: 24px; }
.settings-center__field { display: grid; gap: 8px; min-width: 0; }
.settings-center__field input:not([type=checkbox]), .settings-center__field select, .settings-center__field textarea { width: 100%; box-sizing: border-box; padding: 10px 12px; border: 1px solid var(--color-separator); border-radius: 8px; color: var(--color-label); background: var(--color-bg); }
.settings-center__field input[type=checkbox] { width: 18px; height: 18px; }
.settings-center__field small { color: var(--color-label-secondary); }
.settings-center__clear { display: flex; align-items: center; gap: 8px; }
.settings-center__value { overflow-wrap: anywhere; }
</style>
