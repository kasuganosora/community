<script setup lang="ts">
import { isAxiosError } from 'axios'
import { computed, onUnmounted, reactive, ref, watch } from 'vue'
import { settingsAdminApi as api, type SettingsSection } from '../../infrastructure/settingsAdminApi'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { tokenService } from '@/shared/api/client'
import { AdminAlert, AdminButton, AdminCheck, AdminField, AdminPageHeader } from '../components/ui'
import SettingsAdminPage from './SettingsAdminPage.vue'

const props = defineProps<{ section: string }>()
const data = ref<SettingsSection>()
const draft = reactive<Record<string, string | number | boolean>>({})
const clearSecrets = reactive<Record<string, boolean>>({})
const fieldErrors = reactive<Record<string, string>>({})
const loading = ref(false)
const busy = ref(false)
const testing = ref(false)
const error = ref('')
const notice = ref('')
const testNotice = ref('')
let generation = 0

const lockedFields = computed(() => data.value?.fields.filter(field => field.source === 'environment') || [])
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
  for (const key of Object.keys(fieldErrors)) delete fieldErrors[key]
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
  loading.value = false
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
  for (const key of Object.keys(fieldErrors)) delete fieldErrors[key]
  try {
    const result = await api.save(props.section, changes.value.values, changes.value.clear)
    if (version !== generation || identity !== tokenService.get()) return
    install(result)
    notice.value = restart ? '配置已保存。请由部署管理员重启服务，使新的连接配置生效。' : '设置已保存，新请求立即生效。'
  } catch (cause) {
    if (version !== generation || identity !== tokenService.get()) return
    const status = isAxiosError(cause) ? cause.response?.status : undefined
    const detail = isAxiosError(cause) ? cause.response?.data?.error : undefined
    const invalid = data.value?.fields.find(field => detail === `invalid value for ${field.key}`)
    if (invalid) fieldErrors[invalid.key] = '请检查格式、允许的选项或取值范围。'
    error.value = status === 409 ? '部署配置已锁定此设置。输入已保留，请核对部署配置来源后重试。'
      : status === 403 ? '当前账号没有保存此设置的权限。'
      : '保存失败，输入已保留。请检查字段内容、范围或部署配置覆盖后重试。'
  } finally {
    if (version === generation) busy.value = false
  }
}
async function testConnection() {
  if (testing.value || busy.value || dirty.value || (props.section !== 'email' && props.section !== 'storage')) return
  const version = generation
  const identity = tokenService.get()
  testing.value = true; testNotice.value = ''; error.value = ''
  try {
    const result = await api.test(props.section)
    if (version !== generation || identity !== tokenService.get()) return
    if (result.success) testNotice.value = result.message || '连接测试成功。'
    else error.value = result.message || '连接测试失败，请检查已保存配置。'
  } catch {
    if (version === generation) error.value = '连接测试失败，请检查已保存配置与网络限制后重试。'
  } finally {
    if (version === generation) testing.value = false
  }
}
watch(() => props.section, load, { immediate: true })
watch(sessionEpoch, () => {
  ++generation
  data.value = undefined; error.value = ''; notice.value = ''; testNotice.value = ''
  loading.value = false; busy.value = false; testing.value = false
  for (const key of Object.keys(draft)) delete draft[key]
  for (const key of Object.keys(clearSecrets)) delete clearSecrets[key]
}, { flush: 'sync' })
onUnmounted(() => { generation++; for (const key of Object.keys(draft)) delete draft[key] })
</script>
<template>
  <SettingsAdminPage v-if="section === 'security'" mode="security" />
  <div v-else class="admin-page settings-center">
    <AdminPageHeader :title="data?.title || '设置'" :description="data?.description" />
    <p v-if="loading" class="admin-empty" role="status">正在读取设置…</p>
    <AdminAlert v-if="error" tone="danger">
      {{ error }}
      <template v-if="!data" #action><AdminButton size="sm" :disabled="loading" @click="load">重试</AdminButton></template>
    </AdminAlert>
    <AdminAlert v-if="notice" tone="success">{{ notice }}</AdminAlert>
    <section v-if="lockedFields.length" class="admin-card settings-center__deployment" aria-label="部署配置说明">
      <div><h2>部分配置由部署环境管理</h2><p>以下字段来自服务器环境变量或配置文件，后台不能覆盖。只读值仍可选中复制，密钥不会显示。</p></div>
      <details><summary>如何改为后台管理？</summary><p>由部署管理员先备份现有配置，在部署环境中移除对应配置项并重启服务。回到本页填写并保存配置，测试连接，再按页面提示重启使连接设置生效。切换存储不会迁移已有文件；请先安排文件迁移，避免已有附件无法访问。</p><ul><li v-for="field in lockedFields" :key="field.key">{{ field.label }}：<code>{{ field.deployment_key || field.key }}</code></li></ul></details>
    </section>
    <form v-if="data" class="admin-card admin-preferences" @submit.prevent="save">
      <div class="admin-preferences__heading"><h2>{{ editable ? '配置选项' : '当前配置' }}</h2><span>{{ editable ? '仅保存已修改的字段' : '当前页面为只读状态' }}</span></div>
      <div class="admin-form admin-preferences__fields">
        <template v-for="field in data.fields" :key="field.key">
          <AdminCheck variant="switch" class="admin-preferences__toggle" v-if="field.type === 'boolean' && !field.read_only" :model-value="Boolean(draft[field.key])" @update:model-value="draft[field.key] = $event" :label="field.label" :hint="field.description" :disabled="busy || testing" />
          <AdminField v-else :label="field.label" :error="fieldErrors[field.key]" :hint="[field.description, field.read_only ? (field.source === 'environment' ? '部署配置优先，无法在此编辑。' : '当前运行状态，只读。') : field.restart_required ? '保存后需要重启服务。' : '保存后新请求生效。'].filter(Boolean).join(' ')">
            <template #default="{ id, describedBy }">
              <template v-if="field.read_only">
                <div class="settings-center__readonly">
                  <textarea v-if="field.type === 'textarea'" :id="id" readonly :aria-describedby="describedBy" :aria-invalid="fieldErrors[field.key] ? true : undefined" :value="String(field.value ?? '')" rows="4" />
                  <input v-else :id="id" type="text" readonly :aria-describedby="describedBy" :aria-invalid="fieldErrors[field.key] ? true : undefined" :value="field.type === 'secret' ? (field.secret_set ? '已配置 · 密钥已隐藏' : '未配置密钥') : typeof field.value === 'boolean' ? (field.value ? '开启' : '关闭') : String(field.value ?? '')" placeholder="未配置" />
                  <span class="settings-center__source">{{ field.source === 'environment' ? '部署锁定' : '运行状态' }}</span>
                </div>
              </template>
              <select v-else-if="field.options?.length" :id="id" :aria-describedby="describedBy" :aria-invalid="fieldErrors[field.key] ? true : undefined" v-model="draft[field.key]" :disabled="busy || testing"><option v-for="option in field.options" :key="option" :value="option">{{ option }}</option></select>
              <textarea v-else-if="field.type === 'textarea'" :id="id" :aria-describedby="describedBy" :aria-invalid="fieldErrors[field.key] ? true : undefined" :value="String(draft[field.key] ?? '')" :maxlength="field.max" :disabled="busy || testing" rows="5" @input="draft[field.key] = ($event.target as HTMLTextAreaElement).value" />
              <input v-else-if="field.type === 'number'" :id="id" :aria-describedby="describedBy" :aria-invalid="fieldErrors[field.key] ? true : undefined" v-model.number="draft[field.key]" type="number" required :min="field.min" :max="field.max" :disabled="busy || testing" />
              <template v-else-if="field.type === 'secret'">
                <input :id="id" :aria-describedby="describedBy" :aria-invalid="fieldErrors[field.key] ? true : undefined" v-model="draft[field.key]" type="password" autocomplete="new-password" placeholder="留空保留现有密钥" :disabled="busy || testing || clearSecrets[field.key]" />
                <AdminCheck v-if="field.secret_set" v-model="clearSecrets[field.key]" :label="`明确清除已保存的${field.label}`" :disabled="busy || testing" />
              </template>
              <input v-else :id="id" :aria-describedby="describedBy" :aria-invalid="fieldErrors[field.key] ? true : undefined" v-model="draft[field.key]" :type="field.type" :maxlength="field.max" :disabled="busy || testing" />
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
      <p>测试已保存的配置；不会发送邮件，也不会上传或删除文件。测试成功不代表运行中的连接已切换，连接配置仍需重启生效。</p>
      <p v-if="dirty" class="settings-center__pending" role="status">有未保存的修改，请先保存后再测试连接。</p>
      <div class="admin-form-actions"><AdminButton :loading="testing" :disabled="busy || testing || dirty" @click="testConnection">{{ testing ? '正在测试…' : '测试已保存配置' }}</AdminButton></div>
      <p v-if="testNotice" role="status">{{ testNotice }}</p>
    </section>
  </div>
</template>
<style scoped>
.settings-center__deployment h2 { margin: 0; font-size: 16px; }
.settings-center__readonly { position: relative; min-width: 0; }
.settings-center__readonly :is(input, textarea) { padding-right: 88px; background: var(--color-bg-grouped); color: var(--color-label-secondary); cursor: text; }
.settings-center__source { position: absolute; right: 10px; top: 11px; font-size: 11px; line-height: 18px; padding: 0 6px; border-radius: 4px; background: var(--color-fill); color: var(--color-label-secondary); pointer-events: none; }
.settings-center__deployment { border-inline-start: 3px solid var(--admin-accent); }
.settings-center__deployment p { line-height: 1.7; margin: 8px 0 0; }
.settings-center__deployment summary { cursor: pointer; font-size: 13px; color: var(--admin-accent); min-height: 32px; align-content: center; }
.settings-center__deployment ul { padding-left: 20px; font-size: 12px; line-height: 2; overflow-wrap: anywhere; }
.settings-center__pending { color: var(--color-label-secondary); font-size: 13px; }
@media(max-width: 760px) {
  .settings-center__source { position: static; display: inline-block; margin-top: 6px; }
  .settings-center__readonly :is(input, textarea) { padding-right: 12px; }
}
</style>
