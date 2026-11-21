<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import type { BookmarkFolder, MastodonStatus } from '@/shared/types/mastodon'
import { bookmarkApi } from '../../infrastructure/bookmarkApi'
import { useStatusActions, applyStatusUpdate } from '@/domains/post/presentation/composables/useStatusActions'
import PostCard from '@/shared/components/post/PostCard.vue'
import { MessagePlugin } from '@/shared/ui/feedback'
import { t } from '@/shared/i18n'

const route = useRoute()
const router = useRouter()
const folder = ref<BookmarkFolder | null>(null)
const statuses = ref<MastodonStatus[]>([])
const nextMaxId = ref<string | undefined>(undefined)
const loading = ref(true)
const loadingMore = ref(false)
const error = ref<string | null>(null)
let reqVersion = 0

const { toggleReblog, toggleFavourite, toggleBookmark } = useStatusActions(
  (id, updates) => applyStatusUpdate(statuses.value, id, updates),
)

async function load() {
  const folderId = String(route.params.id ?? '')
  if (!folderId) return
  const version = ++reqVersion
  loading.value = true
  error.value = null
  folder.value = null
  nextMaxId.value = undefined
  try {
    const [meta, page] = await Promise.all([
      bookmarkApi.getFolder(folderId),
      bookmarkApi.listFolderStatuses(folderId, { limit: 40 }),
    ])
    if (version !== reqVersion) return
    folder.value = meta
    statuses.value = page.items
    nextMaxId.value = page.nextCursor
  } catch (e) {
    if (version !== reqVersion) return
    const code = (e as { response?: { status?: number } })?.response?.status
    error.value = code === 404 ? t('bookmark.Folders.notFoundOrPrivate') : (e instanceof Error ? e.message : t('bookmark.BookmarksPage.s2'))
    statuses.value = []
  } finally {
    if (version === reqVersion) loading.value = false
  }
}

async function loadMore() {
  const folderId = String(route.params.id ?? '')
  if (!folderId || !nextMaxId.value || loadingMore.value) return
  const version = reqVersion
  loadingMore.value = true
  try {
    const page = await bookmarkApi.listFolderStatuses(folderId, { max_id: nextMaxId.value, limit: 40 })
    if (version !== reqVersion) return
    statuses.value = [...statuses.value, ...page.items]
    nextMaxId.value = page.nextCursor
  } catch (e) {
    MessagePlugin.error(e instanceof Error ? e.message : t('bookmark.BookmarksPage.s2'))
  } finally {
    if (version === reqVersion) loadingMore.value = false
  }
}

function goBack() {
  if (window.history.state?.back) router.back()
  else void router.push({ path: '/explore' })
}

onMounted(load)
watch(() => route.params.id, () => { void load() })
</script>

<template>
  <div class="folder-share-page">
    <div class="page-header folder-share-page__header">
      <button type="button" class="btn btn--ghost btn--sm" @click="goBack">
        <t-icon name="chevron-left" :size="18" />
      </button>
      <div class="folder-share-page__titles">
        <h1 class="page-header__title">{{ folder?.title || $t('bookmark.Folders.sharedTitle') }}</h1>
        <p v-if="folder?.description" class="folder-share-page__desc">{{ folder.description }}</p>
      </div>
    </div>

    <div v-if="loading" class="page-loading">
      <t-loading />
    </div>

    <div v-else-if="error" class="empty-state" role="alert">
      <div class="empty-state__icon">
        <t-icon name="lock-on" :size="48" />
      </div>
      <h2 class="empty-state__title">{{ $t('bookmark.BookmarksPage.s2') }}</h2>
      <p class="empty-state__text">{{ error }}</p>
      <button type="button" class="btn btn--secondary btn--sm" @click="load">{{ $t('bookmark.BookmarksPage.s3') }}</button>
    </div>

    <template v-else-if="statuses.length">
      <PostCard
        v-for="s in statuses"
        :key="s.id"
        :status="s"
        @reblog="toggleReblog"
        @favourite="toggleFavourite"
        @bookmark="toggleBookmark"
      />
      <div v-if="nextMaxId" class="folder-share-page__more">
        <button type="button" class="btn btn--secondary btn--sm" :disabled="loadingMore" @click="loadMore">
          {{ $t('bookmark.BookmarksPage.s4') }}
        </button>
      </div>
    </template>

    <div v-else class="empty-state">
      <div class="empty-state__icon">
        <t-icon name="folder" :size="48" />
      </div>
      <h2 class="empty-state__title">{{ $t('bookmark.Folders.emptyFolder') }}</h2>
    </div>
  </div>
</template>

<style scoped>
.folder-share-page {
  min-height: 100dvh;
}
.folder-share-page__header {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}
.folder-share-page__titles {
  min-width: 0;
  flex: 1;
}
.folder-share-page__desc {
  margin: 2px 0 0;
  font-size: var(--text-caption1, 12px);
  color: var(--color-label-secondary);
}
.folder-share-page__more {
  display: flex;
  justify-content: center;
  padding: var(--space-4);
}
</style>
