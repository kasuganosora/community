<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { MessagePlugin } from '@/shared/ui/feedback'
import { useSettingsScope } from '../composables/useSettingsScope'
import { moderationApi } from '../../infrastructure/moderationApi'

const domains = ref<string[]>([])
const domain = ref('')
const loading = ref(true)
const saving = ref(false)
const error = ref<string | null>(null)
const loadError = ref('')
const removing = ref('')
const scope = useSettingsScope(() => { domains.value = []; loading.value = false; saving.value = false; removing.value = ''; error.value = null; loadError.value = ''; domain.value = '' })

async function load() {
  const current = scope()
  loading.value = true
  loadError.value = ''
  try {
    const result = await moderationApi.listDomainBlocks()
    if (current()) domains.value = result
  } catch (e) {
    if (current()) loadError.value = e instanceof Error ? e.message : '域名列表加载失败'
  } finally {
    if (current()) loading.value = false
  }
}

async function addDomain() {
  if (saving.value || loading.value || removing.value || loadError.value) return
  const current = scope()
  const value = domain.value.trim().toLowerCase().replace(/^https?:\/\//, '').replace(/\/.*$/, '').replace(/:\d+$/, '')
  if (!value || value.includes(' ') || !value.includes('.')) {
    error.value = '填一个域名，比如 example.com'
    return
  }
  saving.value = true
  error.value = null
  try {
    await moderationApi.blockDomain(value)
    if (!current()) return
    if (!domains.value.includes(value)) domains.value = [value, ...domains.value]
    domain.value = ''
    MessagePlugin.success('已屏蔽这个域名')
  } catch (e) {
    if (current()) error.value = e instanceof Error ? e.message : '屏蔽失败'
  } finally {
    if (current()) saving.value = false
  }
}

async function remove(value: string) {
  if (removing.value || saving.value || loading.value) return
  const current = scope()
  removing.value = value
  try {
    await moderationApi.unblockDomain(value)
    if (!current()) return
    domains.value = domains.value.filter((item) => item !== value)
    MessagePlugin.success('已取消屏蔽')
  } catch (e) {
    if (current()) MessagePlugin.error(e instanceof Error ? e.message : '取消失败')
  } finally {
    if (current()) removing.value = ''
  }
}

onMounted(load)
</script>

<template>
  <form class="settings-form" @submit.prevent="addDomain">
    <p class="settings-field__hint">屏蔽后，这个域名上的账号和帖子不会再出现在你的时间线和搜索里。</p>
    <p v-if="loading" class="settings-field__hint" role="status">正在加载…</p>
    <div v-else-if="loadError" class="settings-error" role="alert">{{ loadError }} <button type="button" class="btn btn--ghost btn--sm" @click="load">重试</button></div>
    <ul v-else-if="domains.length" class="domain-list">
      <li v-for="item in domains" :key="item">
        <span>{{ item }}</span>
        <button type="button" class="btn btn--ghost btn--sm" :disabled="!!removing || saving" :aria-busy="removing === item" @click="remove(item)">{{ removing === item ? '解除中…' : '解除' }}</button>
      </li>
    </ul>
    <p v-else class="settings-field__hint">还没有屏蔽的域名。</p>
    <div class="settings-field">
      <label for="domain-block" class="settings-field__label">域名</label>
      <input id="domain-block" v-model="domain" class="settings-input" type="text" inputmode="url" autocomplete="off" placeholder="example.com" />
    </div>
    <p v-if="error" class="settings-error" role="alert">{{ error }}</p>
    <div class="settings-actions">
      <button type="submit" class="btn btn--primary btn--sm" :disabled="saving || loading || !!removing || !!loadError">{{ saving ? '添加中…' : '屏蔽域名' }}</button>
    </div>
  </form>
</template>

<style scoped>
.settings-form { display: flex; flex-direction: column; gap: var(--space-3); }
.settings-field { display: flex; flex-direction: column; gap: var(--space-1); }
.settings-field__label { font-size: var(--text-footnote, var(--font-sm)); font-weight: 600; }
.settings-field__hint { font-size: var(--text-footnote, var(--font-xs)); color: var(--color-label-secondary, var(--text-secondary)); }
.settings-input {
  width: 100%;
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-separator, var(--border-color));
  border-radius: var(--radius-sm, 8px);
  background: transparent;
  color: inherit;
  font: inherit;
}
.settings-actions { display: flex; justify-content: flex-end; }
.settings-error { color: var(--color-danger, #e0245e); font-size: var(--font-sm); }
.domain-list { display: flex; flex-direction: column; gap: var(--space-2); list-style: none; }
.domain-list li { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); }
</style>
