<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { usePaginatedList } from '@/shared/composables/usePaginatedList'
import { useScrollReturn } from '@/shared/composables/useScrollReturn'
import { bookmarkApi } from '../../infrastructure/bookmarkApi'
import { useStatusActions, applyStatusUpdate } from '@/domains/post/presentation/composables/useStatusActions'
import type { BookmarkFolder, MastodonStatus } from '@/shared/types/mastodon'
import PostCard from '@/shared/components/post/PostCard.vue'
import { DialogPlugin, MessagePlugin } from '@/shared/ui/feedback'
import { t } from '@/shared/i18n'

const feed = usePaginatedList((maxId, limit) => bookmarkApi.list({ max_id: maxId, limit }), 40)
const { items: bookmarks, loading, error, hasMore } = feed
const load = feed.refresh
const { onHeaderTap } = useScrollReturn(() => 'bookmarks')

const folders = ref<BookmarkFolder[]>([])
const activeFolderId = ref<string | null>(null)
const folderStatuses = ref<MastodonStatus[]>([])
const folderNextMaxId = ref<string | undefined>(undefined)
const folderLoading = ref(false)
const folderLoadingMore = ref(false)
const folderError = ref<string | null>(null)
let folderReqVersion = 0

const dialogOpen = ref(false)
const editingFolder = ref<BookmarkFolder | null>(null)
const formTitle = ref('')
const formDesc = ref('')
const formPublic = ref(false)
const saving = ref(false)

const activeFolder = computed(() => folders.value.find(f => f.id === activeFolderId.value) ?? null)

const { toggleReblog, toggleFavourite, toggleBookmark } = useStatusActions((id, updates) => {
  applyStatusUpdate(bookmarks.value, id, updates)
  applyStatusUpdate(folderStatuses.value, id, updates)
})

async function loadFolders() {
  try {
    folders.value = await bookmarkApi.listFolders()
  } catch { /* ignore folder list failure when flat bookmarks still work */ }
}

async function selectFolder(folderId: string | null) {
  const version = ++folderReqVersion
  activeFolderId.value = folderId
  folderNextMaxId.value = undefined
  if (!folderId) return
  folderLoading.value = true
  folderError.value = null
  try {
    const page = await bookmarkApi.listFolderStatuses(folderId, { limit: 40 })
    if (version !== folderReqVersion) return
    folderStatuses.value = page.items
    folderNextMaxId.value = page.nextCursor
  } catch (e) {
    if (version !== folderReqVersion) return
    folderError.value = e instanceof Error ? e.message : t('bookmark.BookmarksPage.s2')
    folderStatuses.value = []
  } finally {
    if (version === folderReqVersion) folderLoading.value = false
  }
}

async function loadMoreFolderStatuses() {
  if (!activeFolderId.value || !folderNextMaxId.value || folderLoadingMore.value) return
  const version = folderReqVersion
  folderLoadingMore.value = true
  try {
    const page = await bookmarkApi.listFolderStatuses(activeFolderId.value, { max_id: folderNextMaxId.value, limit: 40 })
    if (version !== folderReqVersion) return
    folderStatuses.value = [...folderStatuses.value, ...page.items]
    folderNextMaxId.value = page.nextCursor
  } catch (e) {
    MessagePlugin.error(e instanceof Error ? e.message : t('bookmark.BookmarksPage.s2'))
  } finally {
    if (version === folderReqVersion) folderLoadingMore.value = false
  }
}

function openCreateDialog() {
  editingFolder.value = null
  formTitle.value = ''
  formDesc.value = ''
  formPublic.value = false
  dialogOpen.value = true
}

function openEditDialog(folder: BookmarkFolder) {
  editingFolder.value = folder
  formTitle.value = folder.title
  formDesc.value = folder.description
  formPublic.value = folder.is_public
  dialogOpen.value = true
}

async function saveFolder() {
  const title = formTitle.value.trim()
  if (!title || saving.value) return
  saving.value = true
  try {
    if (editingFolder.value) {
      const updated = await bookmarkApi.updateFolder(editingFolder.value.id, {
        title,
        description: formDesc.value.trim(),
        is_public: formPublic.value,
      })
      folders.value = folders.value.map(f => (f.id === updated.id ? updated : f))
      MessagePlugin.success(t('bookmark.Folders.updated'))
    } else {
      const created = await bookmarkApi.createFolder({
        title,
        description: formDesc.value.trim(),
        is_public: formPublic.value,
      })
      folders.value = [...folders.value, created]
      MessagePlugin.success(t('bookmark.Folders.created'))
    }
    dialogOpen.value = false
  } catch (e) {
    MessagePlugin.error(e instanceof Error && e.message ? e.message : t('bookmark.Folders.actionFailed'))
  } finally {
    saving.value = false
  }
}

function confirmDeleteFolder(folder: BookmarkFolder) {
  const dialog = DialogPlugin.confirm({
    header: t('bookmark.Folders.deleteConfirmTitle'),
    body: t('bookmark.Folders.deleteConfirmBody', [folder.title]),
    confirmBtn: { content: t('shared.PostCard.s9'), theme: 'danger' },
    cancelBtn: t('shared.PostCard.s19'),
    onConfirm: async () => {
      try {
        await bookmarkApi.deleteFolder(folder.id)
        folders.value = folders.value.filter(f => f.id !== folder.id)
        if (activeFolderId.value === folder.id) {
          activeFolderId.value = null
          folderStatuses.value = []
        }
        MessagePlugin.success(t('bookmark.Folders.deleted'))
      } catch (e) {
        MessagePlugin.error(e instanceof Error && e.message ? e.message : t('bookmark.Folders.actionFailed'))
      } finally {
        dialog.destroy()
      }
    },
    onClose: () => dialog.destroy(),
  })
}

async function removeFromActiveFolder(statusId: string) {
  if (!activeFolder.value) return
  try {
    const updated = await bookmarkApi.removeFromFolder(activeFolder.value.id, statusId)
    folderStatuses.value = folderStatuses.value.filter(s => s.id !== statusId)
    folders.value = folders.value.map(f => (f.id === updated.id ? updated : f))
  } catch (e) {
    MessagePlugin.error(e instanceof Error && e.message ? e.message : t('bookmark.Folders.actionFailed'))
  }
}

async function copyShareLink(folder: BookmarkFolder) {
  const url = `${window.location.origin}/bookmark_folders/${encodeURIComponent(folder.id)}`
  try {
    await navigator.clipboard.writeText(url)
    MessagePlugin.success(t('bookmark.Folders.linkCopied'))
  } catch {
    MessagePlugin.info(url)
  }
}

onMounted(() => {
  void load()
  void loadFolders()
})
</script>

<template>
  <div class="bookmarks-page">
    <div class="page-header bookmarks-page__header" @click="onHeaderTap">
      <h1 class="page-header__title">{{ $t('bookmark.BookmarksPage.s1') }}</h1>
      <button type="button" class="btn btn--secondary btn--sm" @click.stop="openCreateDialog">
        <t-icon name="folder-add" :size="15" />
        {{ $t('bookmark.Folders.newFolder') }}
      </button>
    </div>

    <div class="folder-tabs" role="tablist">
      <button
        type="button"
        role="tab"
        class="folder-tab"
        :class="{ 'is-active': activeFolderId === null }"
        :aria-selected="activeFolderId === null"
        @click="selectFolder(null)"
      >
        {{ $t('bookmark.Folders.allBookmarks') }}
      </button>
      <button
        v-for="f in folders"
        :key="f.id"
        type="button"
        role="tab"
        class="folder-tab"
        :class="{ 'is-active': activeFolderId === f.id }"
        :aria-selected="activeFolderId === f.id"
        @click="selectFolder(f.id)"
      >
        <t-icon :name="f.is_public ? 'earth' : 'lock-on'" :size="13" />
        <span>{{ f.title }}</span>
        <span class="folder-tab__count">{{ f.items_count }}</span>
      </button>
    </div>

    <!-- Folder view -->
    <template v-if="activeFolder">
      <div class="folder-banner">
        <div class="folder-banner__meta">
          <div class="folder-banner__title-row">
            <strong>{{ activeFolder.title }}</strong>
            <span class="folder-banner__badge">
              {{ activeFolder.is_public ? $t('bookmark.Folders.public') : $t('bookmark.Folders.private') }}
            </span>
          </div>
          <p v-if="activeFolder.description" class="folder-banner__desc">{{ activeFolder.description }}</p>
        </div>
        <div class="folder-banner__actions">
          <button v-if="activeFolder.is_public" type="button" class="btn btn--ghost btn--sm" @click="copyShareLink(activeFolder)">
            <t-icon name="link" :size="14" />
            {{ $t('bookmark.Folders.copyShareLink') }}
          </button>
          <button type="button" class="btn btn--ghost btn--sm" @click="openEditDialog(activeFolder)">
            {{ $t('bookmark.Folders.edit') }}
          </button>
          <button type="button" class="btn btn--ghost btn--sm" @click="confirmDeleteFolder(activeFolder)">
            {{ $t('shared.PostCard.s9') }}
          </button>
        </div>
      </div>

      <div v-if="folderLoading" class="page-loading">
        <t-loading />
      </div>
      <div v-else-if="folderError" class="empty-state" role="alert">
        <p class="empty-state__text">{{ folderError }}</p>
        <button type="button" class="btn btn--secondary btn--sm" @click="selectFolder(activeFolder.id)">{{ $t('bookmark.BookmarksPage.s3') }}</button>
      </div>
      <template v-else-if="folderStatuses.length">
        <div v-for="s in folderStatuses" :key="s.id" class="folder-status-row">
          <div class="folder-status-row__bar">
            <button type="button" class="btn btn--ghost btn--sm" @click="removeFromActiveFolder(s.id)">
              <t-icon name="close" :size="13" />
              {{ $t('bookmark.Folders.removeFromFolder') }}
            </button>
          </div>
          <PostCard
            :status="s"
            @reblog="toggleReblog"
            @favourite="toggleFavourite"
            @bookmark="toggleBookmark"
          />
        </div>
        <div v-if="folderNextMaxId" class="folder-more">
          <button type="button" class="btn btn--secondary btn--sm" :disabled="folderLoadingMore" @click="loadMoreFolderStatuses">
            {{ $t('bookmark.BookmarksPage.s4') }}
          </button>
        </div>
      </template>
      <div v-else class="empty-state">
        <div class="empty-state__icon">
          <t-icon name="folder" :size="48" />
        </div>
        <h2 class="empty-state__title">{{ $t('bookmark.Folders.emptyFolder') }}</h2>
        <p class="empty-state__text">{{ $t('bookmark.Folders.emptyFolderHint') }}</p>
      </div>
    </template>

    <!-- All bookmarks view -->
    <template v-else>
      <div v-if="loading && !bookmarks.length" class="page-loading">
        <t-loading />
      </div>

      <div v-else-if="error && !bookmarks.length" class="empty-state" role="alert">
        <div class="empty-state__icon">
          <t-icon name="error-circle" :size="48" />
        </div>
        <h2 class="empty-state__title">{{ $t('bookmark.BookmarksPage.s2') }}</h2>
        <p class="empty-state__text">{{ error }}</p>
        <button type="button" class="btn btn--secondary btn--sm" @click="load">{{ $t('bookmark.BookmarksPage.s3') }}</button>
      </div>

      <template v-else-if="bookmarks.length">
        <PostCard
          v-for="s in bookmarks"
          :key="s.id"
          :status="s"
          @reblog="toggleReblog"
          @favourite="toggleFavourite"
          @bookmark="toggleBookmark"
        />
        <p v-if="error" role="alert">{{ error }}</p>
        <button v-if="hasMore" :disabled="loading" type="button" class="btn btn--secondary btn--sm" @click="feed.loadMore">{{ $t('bookmark.BookmarksPage.s4') }}</button>
      </template>

      <div v-else class="empty-state">
        <div class="empty-state__icon">
          <t-icon name="bookmark" :size="48" />
        </div>
        <h2 class="empty-state__title">{{ $t('bookmark.BookmarksPage.s5') }}</h2>
        <p class="empty-state__text">
          {{ $t('bookmark.BookmarksPage.s6') }}
        </p>
      </div>
    </template>

    <t-dialog
      v-model:visible="dialogOpen"
      :header="editingFolder ? $t('bookmark.Folders.editFolder') : $t('bookmark.Folders.newFolder')"
      :footer="false"
      width="420px"
    >
      <form class="folder-form" @submit.prevent="saveFolder">
        <label class="folder-form__field">
          <span>{{ $t('bookmark.Folders.titleLabel') }}</span>
          <input v-model="formTitle" type="text" maxlength="64" required :placeholder="$t('bookmark.Folders.newFolderPlaceholder')" />
        </label>
        <label class="folder-form__field">
          <span>{{ $t('bookmark.Folders.descLabel') }}</span>
          <textarea v-model="formDesc" rows="2" maxlength="256" :placeholder="$t('bookmark.Folders.descPlaceholder')" />
        </label>
        <label class="folder-form__checkbox">
          <input v-model="formPublic" type="checkbox" />
          <span>{{ $t('bookmark.Folders.publicToggleHint') }}</span>
        </label>
        <div class="folder-form__actions">
          <button type="button" class="btn btn--secondary btn--sm" @click="dialogOpen = false">{{ $t('shared.PostCard.s19') }}</button>
          <button type="submit" class="btn btn--primary btn--sm" :disabled="!formTitle.trim() || saving">{{ $t('bookmark.Folders.save') }}</button>
        </div>
      </form>
    </t-dialog>
  </div>
</template>

<style scoped>
.bookmarks-page {
  min-height: 100dvh;
}
.bookmarks-page__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}
.folder-tabs {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--gutter);
  overflow-x: auto;
  border-bottom: 1px solid var(--color-separator);
}
.folder-tab {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 5px 12px;
  border: 1px solid var(--color-separator);
  border-radius: var(--radius-full, 999px);
  background: transparent;
  color: var(--color-label-secondary);
  font-size: var(--text-subhead, 13px);
  white-space: nowrap;
  cursor: pointer;
}
.folder-tab.is-active {
  background: var(--color-accent-soft, rgba(0, 122, 255, 0.12));
  border-color: var(--color-accent, #007aff);
  color: var(--color-accent-text, #007aff);
  font-weight: var(--weight-semibold, 600);
}
.folder-tab__count {
  opacity: 0.75;
  font-size: var(--text-caption1, 12px);
}
.folder-banner {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  padding: var(--space-3) var(--gutter);
  border-bottom: 1px solid var(--color-separator);
  background: var(--color-fill-tertiary, rgba(128, 128, 128, 0.05));
}
.folder-banner__title-row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}
.folder-banner__badge {
  font-size: var(--text-caption1, 12px);
  padding: 1px 7px;
  border-radius: var(--radius-full, 999px);
  background: var(--color-fill-secondary, rgba(128, 128, 128, 0.14));
  color: var(--color-label-secondary);
}
.folder-banner__desc {
  margin: 4px 0 0;
  font-size: var(--text-subhead, 13px);
  color: var(--color-label-secondary);
}
.folder-banner__actions {
  display: flex;
  align-items: center;
  gap: var(--space-1);
}
.folder-status-row__bar {
  display: flex;
  justify-content: flex-end;
  padding: var(--space-1) var(--gutter) 0;
}
.folder-more {
  display: flex;
  justify-content: center;
  padding: var(--space-4);
}
.folder-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}
.folder-form__field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: var(--text-subhead, 13px);
}
.folder-form__field input,
.folder-form__field textarea {
  padding: 8px 10px;
  border: 1px solid var(--color-separator);
  border-radius: var(--radius-sm, 8px);
  background: var(--color-bg-primary, transparent);
  color: var(--color-label);
}
.folder-form__checkbox {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: var(--text-subhead, 13px);
  color: var(--color-label-secondary);
  cursor: pointer;
}
.folder-form__actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
}
</style>
