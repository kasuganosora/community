<script setup lang="ts">
import { ref, onMounted } from 'vue'
import type { HistoryItem } from '@/shared/types/mastodon'
import { historyApi } from '../../infrastructure/historyApi'
import { useStatusActions, applyStatusUpdate } from '@/domains/post/presentation/composables/useStatusActions'
import { useScrollReturn } from '@/shared/composables/useScrollReturn'
import PostCard from '@/shared/components/post/PostCard.vue'
import { DialogPlugin, MessagePlugin } from '@/shared/ui/feedback'
import { t } from '@/shared/i18n'

const items = ref<HistoryItem[]>([])
const loading = ref(true)
const loadingMore = ref(false)
const hasMore = ref(false)
const error = ref<string | null>(null)
const paused = ref(false)
const settingsBusy = ref(false)

const { onHeaderTap } = useScrollReturn(() => 'history')

let reqVersion = 0

const { toggleReblog, toggleFavourite, toggleBookmark } = useStatusActions((id, updates) => {
  for (const entry of items.value) {
    applyStatusUpdate([entry.status], id, updates)
  }
})

async function loadInitial() {
  const version = ++reqVersion
  loading.value = true
  error.value = null
  try {
    const [list, settings] = await Promise.all([
      historyApi.list({ limit: 20 }),
      historyApi.getSettings(),
    ])
    if (version !== reqVersion) return
    items.value = list
    hasMore.value = list.length >= 20
    paused.value = settings.paused
  } catch (e) {
    if (version === reqVersion) {
      error.value = e instanceof Error ? e.message : t('history.HistoryPage.loadFailed')
    }
  } finally {
    if (version === reqVersion) loading.value = false
  }
}

async function loadMore() {
  if (loadingMore.value || !items.value.length) return
  const lastCursor = items.value[items.value.length - 1]?.cursor
  if (!lastCursor) return
  const version = reqVersion
  loadingMore.value = true
  try {
    const next = await historyApi.list({ max_cursor: lastCursor, limit: 20 })
    if (version !== reqVersion) return
    items.value = [...items.value, ...next]
    hasMore.value = next.length >= 20
  } catch (e) {
    MessagePlugin.error(e instanceof Error ? e.message : t('history.HistoryPage.loadFailed'))
  } finally {
    if (version === reqVersion) loadingMore.value = false
  }
}

async function togglePaused() {
  if (settingsBusy.value) return
  settingsBusy.value = true
  const next = !paused.value
  try {
    const res = await historyApi.updateSettings(next)
    paused.value = res.paused
    MessagePlugin.success(res.paused ? t('history.HistoryPage.pausedToast') : t('history.HistoryPage.resumedToast'))
  } catch (e) {
    MessagePlugin.error(e instanceof Error ? e.message : t('history.HistoryPage.actionFailed'))
  } finally {
    settingsBusy.value = false
  }
}

async function removeEntry(statusId: string) {
  try {
    await historyApi.remove(statusId)
    items.value = items.value.filter(i => i.status.id !== statusId)
  } catch (e) {
    MessagePlugin.error(e instanceof Error ? e.message : t('history.HistoryPage.actionFailed'))
  }
}

function confirmClear() {
  const dialog = DialogPlugin.confirm({
    header: t('history.HistoryPage.clearConfirmTitle'),
    body: t('history.HistoryPage.clearConfirmBody'),
    confirmBtn: { content: t('history.HistoryPage.clearAll'), theme: 'danger' },
    cancelBtn: t('shared.PostCard.s19'),
    onConfirm: async () => {
      try {
        await historyApi.clear()
        ++reqVersion
        items.value = []
        hasMore.value = false
        MessagePlugin.success(t('history.HistoryPage.cleared'))
      } catch (e) {
        MessagePlugin.error(e instanceof Error ? e.message : t('history.HistoryPage.actionFailed'))
      } finally {
        dialog.destroy()
      }
    },
    onClose: () => dialog.destroy(),
  })
}

function formatViewedAt(iso: string): string {
  try {
    return new Date(iso).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })
  } catch {
    return ''
  }
}

onMounted(loadInitial)
</script>

<template>
  <div class="history-page">
    <div class="page-header history-page__header" @click="onHeaderTap">
      <h1 class="page-header__title">{{ $t('history.HistoryPage.title') }}</h1>
      <div class="history-page__actions" @click.stop>
        <button type="button" class="btn btn--secondary btn--sm" :disabled="settingsBusy" @click="togglePaused">
          <t-icon :name="paused ? 'play-circle' : 'pause-circle'" :size="15" />
          {{ paused ? $t('history.HistoryPage.resume') : $t('history.HistoryPage.pause') }}
        </button>
        <button v-if="items.length" type="button" class="btn btn--ghost btn--sm" @click="confirmClear">
          <t-icon name="delete" :size="15" />
          {{ $t('history.HistoryPage.clearAll') }}
        </button>
      </div>
    </div>

    <div v-if="paused" class="history-page__banner" role="status">
      <t-icon name="pause-circle" :size="16" />
      <span>{{ $t('history.HistoryPage.pausedBanner') }}</span>
    </div>

    <div v-if="loading && !items.length" class="page-loading">
      <t-loading />
    </div>

    <div v-else-if="error && !items.length" class="empty-state" role="alert">
      <div class="empty-state__icon">
        <t-icon name="error-circle" :size="48" />
      </div>
      <h2 class="empty-state__title">{{ $t('history.HistoryPage.loadFailed') }}</h2>
      <p class="empty-state__text">{{ error }}</p>
      <button type="button" class="btn btn--secondary btn--sm" @click="loadInitial">{{ $t('bookmark.BookmarksPage.s3') }}</button>
    </div>

    <template v-else-if="items.length">
      <div v-for="entry in items" :key="`${entry.status.id}-${entry.cursor}`" class="history-entry">
        <div class="history-entry__bar">
          <span class="history-entry__time">
            <t-icon name="time" :size="13" />
            {{ $t('history.HistoryPage.viewedAt', [formatViewedAt(entry.viewed_at)]) }}
          </span>
          <button
            type="button"
            class="history-entry__remove"
            :aria-label="$t('history.HistoryPage.removeOne')"
            @click="removeEntry(entry.status.id)"
          >
            <t-icon name="close" :size="14" />
          </button>
        </div>
        <PostCard
          :status="entry.status"
          @reblog="toggleReblog"
          @favourite="toggleFavourite"
          @bookmark="toggleBookmark"
        />
      </div>

      <div v-if="hasMore" class="history-page__more">
        <button type="button" class="btn btn--secondary btn--sm" :disabled="loadingMore" @click="loadMore">
          {{ $t('bookmark.BookmarksPage.s4') }}
        </button>
      </div>
    </template>

    <div v-else class="empty-state">
      <div class="empty-state__icon">
        <t-icon name="time" :size="48" />
      </div>
      <h2 class="empty-state__title">{{ $t('history.HistoryPage.emptyTitle') }}</h2>
      <p class="empty-state__text">{{ $t('history.HistoryPage.emptyDesc') }}</p>
    </div>
  </div>
</template>

<style scoped>
.history-page {
  min-height: 100dvh;
}
.history-page__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}
.history-page__actions {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}
.history-page__banner {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--gutter);
  font-size: var(--text-subhead, 13px);
  color: var(--color-label-secondary);
  background: var(--color-fill-tertiary, rgba(128, 128, 128, 0.08));
  border-bottom: 1px solid var(--color-separator);
}
.history-entry__bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-2) var(--gutter) 0;
  font-size: var(--text-caption1, 12px);
  color: var(--color-label-secondary);
}
.history-entry__time {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}
.history-entry__remove {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border: none;
  border-radius: 50%;
  background: transparent;
  color: var(--color-label-secondary);
  cursor: pointer;
}
.history-entry__remove:hover {
  background: var(--color-fill-tertiary, rgba(128, 128, 128, 0.12));
  color: var(--color-label);
}
.history-page__more {
  display: flex;
  justify-content: center;
  padding: var(--space-4);
}
</style>
