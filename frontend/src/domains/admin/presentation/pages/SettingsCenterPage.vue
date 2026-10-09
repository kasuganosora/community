<script setup lang="ts">
import { computed, onUnmounted, reactive, ref, watch } from 'vue'
import { settingsAdminApi as api, type SettingsSection } from '../../infrastructure/settingsAdminApi'
import { tokenService } from '@/shared/api/client'
import { AdminAlert, AdminButton, AdminCheck, AdminField, AdminPageHeader } from '../components/ui'
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
    <AdminPageHeader :title="data?.title || '设置'" :description="data?.description" />
    <p v-if="loading" class="admin-empty" role="status">正在读取设置…</p>
    <AdminAlert v-if="error" tone="danger">
      {{ error }}
      <template v-if="!data" #action><AdminButton size="sm" :disabled="loading" @click="load">重试</AdminButton></template>
    </AdminAlert>
    <AdminAlert v-if="notice" tone="success">{{ notice }}</AdminAlert>
    <form v-if="data" class="admin-card" @submit.prevent="save">
      <div class="admin-form">
        <template v-for="field in data.fields" :key="field.key">
          <AdminCheck v-if="field.type === 'boolean' && !field.read_only" :model-value="Boolean(draft[field.key])" @update:model-value="draft[field.key] = $event" :label="field.label" :hint="field.description" :disabled="busy || testing" />
          <AdminField v-else :label="field.label" :hint="field.read_only ? (field.source === 'environment' ? '由部署环境管理，请修改服务器配置。' : '当前运行状态，只读。') : field.restart_required ? '保存后需要重启服务。' : field.description">
            <template #default="{ id }">
              <template v-if="field.read_only">
                <p v-if="field.type === 'secret'" :id="id" class="settings-center__value">{{ field.secret_set ? '已配置密钥' : '未配置密钥' }}</p>
                <p v-else :id="id" class="settings-center__value">{{ typeof field.value === 'boolean' ? (field.value ? '开启' : '关闭') : field.value ?? '未配置' }}</p>
              </template>
              <select v-else-if="field.options?.length" :id="id" v-model="draft[field.key]" :disabled="busy || testing"><option v-for="option in field.options" :key="option" :value="option">{{ option }}</option></select>
              <textarea v-else-if="field.type === 'textarea'" :id="id" :value="String(draft[field.key] ?? '')" :maxlength="field.max" :disabled="busy || testing" rows="5" @input="draft[field.key] = ($event.target as HTMLTextAreaElement).value" />
              <input v-else-if="field.type === 'number'" :id="id" v-model.number="draft[field.key]" type="number" required :min="field.min" :max="field.max" :disabled="busy || testing" />
              <template v-else-if="field.type === 'secret'">
                <input :id="id" v-model="draft[field.key]" type="password" autocomplete="new-password" placeholder="留空保留现有密钥" :disabled="busy || testing || clearSecrets[field.key]" />
                <AdminCheck v-if="field.secret_set" v-model="clearSecrets[field.key]" :label="`明确清除已保存的${field.label}`" :disabled="busy || testing" />
              </template>
              <input v-else :id="id" v-model="draft[field.key]" :type="field.type" :maxlength="field.max" :disabled="busy || testing" />
            </template>
          </AdminField>
        </template>
      </div>
      <div class="admin-form-actions">
        <AdminButton v-if="editable" type="submit" variant="primary" :loading="busy" :disabled="testing || !dirty">{{ busy ? '保存中…' : '保存设置' }}</AdminButton>
        <AdminButton :disabled="busy || testing || dirty" @click="load">刷新</AdminButton>
      </div>
    </form>
    <section v-if="connectionSection && data" class="admin-card">
      <h2>连接测试</h2>
      <p>测试已保存的配置；不会发送邮件，也不会上传或删除文件。</p>
      <div class="admin-form-actions"><AdminButton :loading="testing" :disabled="busy || dirty" @click="testConnection">{{ testing ? '正在测试…' : '测试已保存配置' }}</AdminButton></div>
      <p v-if="testNotice" role="status">{{ testNotice }}</p>
    </section>
  </div>
</template>
<style scoped>
.settings-center__value { margin: 0; min-height: 38px; display: flex; align-items: center; overflow-wrap: anywhere; color: var(--color-label); font-weight: 400; font-size: 14px; }
</style>
