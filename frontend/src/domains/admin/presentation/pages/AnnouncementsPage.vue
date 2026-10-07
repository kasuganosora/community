<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { announcementsAuditApi as api, type Announcement } from '../../infrastructure/announcementsAuditApi'
const rows = ref<Announcement[]>([])
const unreadOnly = ref(false)
const loading = ref(false)
const dismissing = ref<string[]>([])
const error = ref('')
async function load() {
  if (loading.value || dismissing.value.length) return
  loading.value = true; error.value = ''
  try { rows.value = await api.visible(unreadOnly.value) } catch (e) { error.value = e instanceof Error ? e.message : '无法读取公告' } finally { loading.value = false }
}
async function dismiss(row: Announcement) {
  if (loading.value || dismissing.value.includes(row.id)) return
  dismissing.value.push(row.id); error.value = ''
  try { await api.dismiss(row.id); row.read = true; if (unreadOnly.value) rows.value = rows.value.filter(item => item.id !== row.id) } catch (e) { error.value = e instanceof Error ? e.message : '未能标记已读，请重试' } finally { dismissing.value = dismissing.value.filter(id => id !== row.id) }
}
onMounted(load)
</script>
<template>
  <main class="announcements-page">
    <h1>站点公告</h1>
    <label><input v-model="unreadOnly" type="checkbox" :disabled="loading || dismissing.length > 0" @change="load" />只看未读</label>
    <button type="button" :disabled="loading || dismissing.length > 0" @click="load">刷新</button>
    <p v-if="error" role="alert">{{ error }}</p>
    <p v-if="loading" role="status">正在加载公告…</p>
    <p v-else-if="!rows.length">{{ unreadOnly ? '没有未读公告' : '暂无公告' }}</p>
    <article v-for="row in rows" :key="row.id">
      <h2>公告 #{{ row.id }} <small>{{ row.read ? '已读' : '未读' }}</small></h2>
      <p class="announcement-text">{{ row.text ?? row.content }}</p>
      <time :datetime="row.updated_at">{{ new Date(row.updated_at).toLocaleString() }}</time>
      <button v-if="!row.read" type="button" :disabled="loading || dismissing.includes(row.id)" @click="dismiss(row)">标记已读</button>
    </article>
  </main>
</template>
<style scoped>
.announcements-page { max-width: 800px; padding: 24px; margin: auto; }
article { border: 1px solid var(--td-component-border); border-radius: 12px; padding: 20px; margin: 20px 0; }
.announcement-text { white-space: pre-wrap; overflow-wrap: anywhere; }
button { margin: 8px; padding: 8px 12px; }
small { font-weight: normal; font-size: 14px; }
[role='alert'] { color: var(--td-error-color); }
</style>
