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
    <div class="page-header page-header--with-back">
      <button class="page-header__back-btn" type="button" aria-label="返回" @click="$router.back()">
        <t-icon name="chevron-left" :size="20" />
      </button>
      <h1 class="page-header__title">站点公告</h1>
    </div>
    <div class="announcements-toolbar">
      <label class="announcements-filter">
        <input v-model="unreadOnly" type="checkbox" :disabled="loading || dismissing.length > 0" @change="load" />
        <span>只看未读</span>
      </label>
      <button type="button" class="btn btn--secondary btn--sm" :disabled="loading || dismissing.length > 0" @click="load">
        <t-icon name="refresh" :size="16" />
        刷新
      </button>
    </div>
    <p v-if="error" class="announcements-error" role="alert">{{ error }}</p>
    <p v-if="loading" class="announcements-status" role="status">正在加载公告…</p>
    <div v-else-if="!rows.length" class="empty-state">
      <div class="empty-state__icon"><t-icon name="notification" /></div>
      <p class="empty-state__title">{{ unreadOnly ? '没有未读公告' : '暂无公告' }}</p>
    </div>
    <article v-for="row in rows" :key="row.id" class="announcement" :class="{ 'announcement--unread': !row.read }">
      <header class="announcement__meta">
        <span class="announcement__badge">{{ row.read ? '已读' : '未读' }}</span>
        <time :datetime="row.updated_at">{{ new Date(row.updated_at).toLocaleString() }}</time>
      </header>
      <p class="announcement__text">{{ row.text ?? row.content }}</p>
      <button v-if="!row.read" type="button" class="btn btn--secondary btn--sm" :disabled="loading || dismissing.includes(row.id)" @click="dismiss(row)">标记已读</button>
    </article>
  </main>
</template>
<style scoped>
.announcements-page { min-height: 100vh; min-height: 100dvh; }
.announcements-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  padding: var(--space-3) var(--gutter);
}
.announcements-filter {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  color: var(--color-label);
  font-size: var(--text-subhead);
  cursor: pointer;
}
.announcements-filter input { width: 1.125rem; height: 1.125rem; margin: 0; accent-color: var(--color-accent); }
.announcements-toolbar .btn { gap: var(--space-1); }
.announcements-status,
.announcements-error { padding: var(--space-4) var(--gutter); color: var(--color-label-secondary); font-size: var(--text-subhead); }
.announcements-error { color: var(--color-danger); }
.announcement {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--space-2);
  margin: 0 var(--gutter) var(--space-3);
  padding: var(--space-4);
  border-radius: var(--radius-lg);
  background: var(--color-fill);
}
.announcement__meta { display: flex; align-items: center; gap: var(--space-2); color: var(--color-label-secondary); font-size: var(--text-footnote); }
.announcement__badge { padding: 0 var(--space-2); border-radius: var(--radius-full); background: var(--color-bg); }
.announcement--unread .announcement__badge { background: var(--color-accent); color: var(--color-on-accent); }
.announcement__text { white-space: pre-wrap; overflow-wrap: anywhere; color: var(--color-label); font-size: var(--text-body); line-height: var(--leading-body); }
</style>
