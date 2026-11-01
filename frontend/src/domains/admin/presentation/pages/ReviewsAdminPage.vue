<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { reviewAdminApi, type ReviewJob } from '../../infrastructure/reviewAdminApi'
import { AdminAlert, AdminButton, AdminPageHeader } from '../components/ui'
import { t } from '@/shared/i18n'

const items = ref<ReviewJob[]>([])
const cursors = ref<(string | undefined)[]>([undefined])
const hasMore = ref(false)
const enabled = ref(true)
const configured = ref(true)
const loading = ref(false)
const busy = ref('')
const error = ref('')
const notice = ref('')
let request = 0

async function load(next = cursors.value) {
  const version = ++request
  loading.value = true
  error.value = ''
  try {
    const result = await reviewAdminApi.list(next.at(-1))
    if (version !== request) return
    items.value = result.items
    hasMore.value = result.has_more
    enabled.value = result.enabled
    configured.value = result.configured
    cursors.value = next
  } catch (e) {
    if (version === request) error.value = e instanceof Error ? e.message : t('admin.Reviews.error')
  } finally {
    if (version === request) loading.value = false
  }
}
function state(job: ReviewJob) {
  if (job.verdict === 'flag') return t('admin.Reviews.flag')
  if (job.last_error === 'llm not configured') return t('admin.Reviews.unconfigured')
  return t(`admin.Reviews.${['pending', 'processing', 'failed'].includes(job.state) ? job.state : 'pending'}`)
}
async function act(job: ReviewJob, action: 'approve' | 'retry') {
  if (busy.value || loading.value || !job.actionable) return
  if (action === 'approve' && !window.confirm(t('admin.Reviews.confirm', [job.acct]))) return
  busy.value = job.id
  error.value = ''
  notice.value = ''
  try {
    await reviewAdminApi.act(job.id, action)
    notice.value = t(action === 'approve' ? 'admin.Reviews.approved' : 'admin.Reviews.retried')
    await load()
  } catch (e) {
    error.value = e instanceof Error ? e.message : t('admin.Reviews.error')
  } finally {
    busy.value = ''
  }
}
function nextPage() {
  const last = items.value.at(-1)
  if (last) void load([...cursors.value, last.id])
}
onMounted(() => load())
onBeforeUnmount(() => { request++ })
</script>

<template>
  <section class="admin-page reviews-page">
    <AdminPageHeader :title="$t('admin.Reviews.title')" :description="$t('admin.Reviews.description')">
      <AdminButton :disabled="loading || !!busy" @click="load([undefined])">{{ $t('admin.Reviews.refresh') }}</AdminButton>
    </AdminPageHeader>
    <AdminAlert v-if="!enabled || !configured" tone="warning">{{ !enabled ? $t('admin.Reviews.disabled') : $t('admin.Reviews.unconfiguredHint') }}</AdminAlert>
    <AdminAlert v-if="error" tone="danger">{{ error }}</AdminAlert>
    <AdminAlert v-if="notice" tone="success">{{ notice }}</AdminAlert>
    <p v-if="loading" role="status" class="reviews-loading">{{ $t('admin.Reviews.loading') }}</p>
    <div class="review-list" :aria-busy="loading">
      <article v-for="job in items" :key="job.id" class="admin-card review-card">
        <header class="review-card__header">
          <div>
            <strong>{{ job.display_name || job.acct || $t('admin.Reviews.unavailable') }}</strong>
            <span v-if="job.acct" class="review-acct">@{{ job.acct }}</span>
          </div>
          <div class="review-actions">
            <router-link v-if="job.acct" :to="`/@${job.acct}/status/${job.status_id}`">{{ $t('admin.Reviews.viewPost') }}</router-link>
            <span class="admin-badge">{{ state(job) }}</span>
          </div>
        </header>
        <p class="review-text">{{ job.text || $t('admin.Reviews.emptyPost') }}</p>
        <p v-if="job.reason || job.last_error" class="review-reason">{{ job.last_error === 'llm not configured' ? $t('admin.Reviews.unconfigured') : job.reason || job.last_error }}</p>
        <footer class="review-card__footer">
          <span>{{ job.held ? $t('admin.Reviews.held') : $t('admin.Reviews.notHeld') }}</span>
          <div class="review-actions">
            <AdminButton :disabled="loading || !!busy || !job.actionable || !enabled || !configured" @click="act(job, 'retry')">{{ $t('admin.Reviews.retry') }}</AdminButton>
            <AdminButton variant="primary" :loading="busy === job.id" :disabled="loading || !!busy || !job.actionable" @click="act(job, 'approve')">{{ $t('admin.Reviews.approve') }}</AdminButton>
          </div>
        </footer>
      </article>
      <p v-if="!loading && !error && !items.length" class="admin-card admin-empty">{{ $t('admin.Reviews.empty') }}</p>
    </div>
    <nav class="admin-pagination" :aria-label="$t('admin.AppealsAdminPage.s19')">
      <AdminButton :disabled="loading || !!busy || cursors.length === 1" @click="load(cursors.slice(0, -1))">{{ $t('admin.AppealsAdminPage.s20') }}</AdminButton>
      <span>{{ $t('admin.AppealsAdminPage.s21', [cursors.length]) }}</span>
      <AdminButton :disabled="loading || !!busy || !hasMore" @click="nextPage">{{ $t('admin.AppealsAdminPage.s22') }}</AdminButton>
    </nav>
  </section>
</template>

<style scoped>
.review-list { display: grid; gap: 16px; }
.review-card { margin: 0; }
.review-card__header, .review-card__footer { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; }
.review-acct { display: block; color: var(--color-label-secondary); font-size: 13px; overflow-wrap: anywhere; }
.review-text { white-space: pre-wrap; overflow-wrap: anywhere; max-height: 20rem; overflow-y: auto; line-height: 1.65; }
.review-reason { padding: 12px; border-radius: 10px; background: var(--color-bg-grouped); overflow-wrap: anywhere; }
.review-card__footer { color: var(--color-label-secondary); font-size: 13px; border-top: 1px solid var(--color-separator); padding-top: 12px; }
.review-actions { display: flex; gap: 8px; flex-wrap: wrap; }
.reviews-loading { margin: 0; color: var(--color-label-secondary); }
</style>
