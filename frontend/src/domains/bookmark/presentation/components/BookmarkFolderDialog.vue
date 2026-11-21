<script setup lang="ts">
import { ref, watch } from 'vue'
import type { BookmarkFolder } from '@/shared/types/mastodon'
import { bookmarkApi } from '../../infrastructure/bookmarkApi'
import { MessagePlugin } from '@/shared/ui/feedback'
import { t } from '@/shared/i18n'

const props = defineProps<{
  modelValue: boolean
  statusId: string
}>()

const emit = defineEmits<{
  'update:modelValue': [visible: boolean]
}>()

const folders = ref<BookmarkFolder[]>([])
const loading = ref(false)
const busyId = ref<string | null>(null)
const newTitle = ref('')
const newPublic = ref(false)
const creating = ref(false)

let loadVersion = 0

async function loadFolders() {
  if (!props.statusId) return
  const version = ++loadVersion
  loading.value = true
  try {
    const list = await bookmarkApi.listFolders(props.statusId)
    if (version !== loadVersion) return
    folders.value = list
  } catch (e) {
    if (version === loadVersion) {
      MessagePlugin.error(e instanceof Error && e.message ? e.message : t('bookmark.Folders.loadFailed'))
    }
  } finally {
    if (version === loadVersion) loading.value = false
  }
}

watch(
  () => [props.modelValue, props.statusId] as const,
  ([open]) => {
    if (open) void loadFolders()
  },
  { immediate: true },
)

async function toggleMembership(folder: BookmarkFolder) {
  if (busyId.value) return
  busyId.value = folder.id
  try {
    const updated = folder.has_status
      ? await bookmarkApi.removeFromFolder(folder.id, props.statusId)
      : await bookmarkApi.addToFolder(folder.id, props.statusId)
    folder.has_status = !folder.has_status
    folder.items_count = updated.items_count
  } catch (e) {
    MessagePlugin.error(e instanceof Error && e.message ? e.message : t('bookmark.Folders.actionFailed'))
  } finally {
    busyId.value = null
  }
}

async function createAndAdd() {
  const title = newTitle.value.trim()
  if (!title || creating.value) return
  creating.value = true
  try {
    const created = await bookmarkApi.createFolder({ title, is_public: newPublic.value })
    const updated = await bookmarkApi.addToFolder(created.id, props.statusId)
    folders.value = [...folders.value, { ...updated, has_status: true }]
    newTitle.value = ''
    newPublic.value = false
    MessagePlugin.success(t('bookmark.Folders.created'))
  } catch (e) {
    MessagePlugin.error(e instanceof Error && e.message ? e.message : t('bookmark.Folders.actionFailed'))
  } finally {
    creating.value = false
  }
}
</script>

<template>
  <t-dialog
    :visible="modelValue"
    :header="$t('bookmark.Folders.addToFolderTitle')"
    :footer="false"
    width="420px"
    @update:visible="(v: boolean) => emit('update:modelValue', v)"
  >
    <div class="folder-dialog">
      <div v-if="loading" class="folder-dialog__loading">
        <t-loading size="small" />
      </div>
      <div v-else-if="folders.length" class="folder-dialog__list">
        <button
          v-for="f in folders"
          :key="f.id"
          type="button"
          class="folder-dialog__row"
          :class="{ 'is-selected': f.has_status }"
          :disabled="busyId === f.id"
          @click="toggleMembership(f)"
        >
          <span class="folder-dialog__check">
            <t-icon :name="f.has_status ? 'check-circle-filled' : 'circle'" :size="18" />
          </span>
          <span class="folder-dialog__info">
            <span class="folder-dialog__title">{{ f.title }}</span>
            <span class="folder-dialog__meta">
              {{ f.is_public ? $t('bookmark.Folders.public') : $t('bookmark.Folders.private') }} · {{ f.items_count }}
            </span>
          </span>
        </button>
      </div>
      <p v-else class="folder-dialog__empty">{{ $t('bookmark.Folders.noFoldersYet') }}</p>

      <form class="folder-dialog__create" @submit.prevent="createAndAdd">
        <input
          v-model="newTitle"
          type="text"
          class="folder-dialog__input"
          maxlength="64"
          :placeholder="$t('bookmark.Folders.newFolderPlaceholder')"
        />
        <label class="folder-dialog__public">
          <input v-model="newPublic" type="checkbox" />
          <span>{{ $t('bookmark.Folders.public') }}</span>
        </label>
        <button type="submit" class="btn btn--primary btn--sm" :disabled="!newTitle.trim() || creating">
          {{ $t('bookmark.Folders.createAndAdd') }}
        </button>
      </form>
    </div>
  </t-dialog>
</template>

<style scoped>
.folder-dialog {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}
.folder-dialog__loading {
  display: flex;
  justify-content: center;
  padding: var(--space-4);
}
.folder-dialog__list {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  max-height: 260px;
  overflow-y: auto;
}
.folder-dialog__row {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  width: 100%;
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-separator);
  border-radius: var(--radius-md, 10px);
  background: transparent;
  color: var(--color-label);
  text-align: left;
  cursor: pointer;
}
.folder-dialog__row:hover {
  background: var(--color-fill-tertiary, rgba(128, 128, 128, 0.08));
}
.folder-dialog__row.is-selected {
  border-color: var(--color-accent, #007aff);
}
.folder-dialog__check {
  color: var(--color-accent, #007aff);
  display: inline-flex;
}
.folder-dialog__info {
  display: flex;
  flex-direction: column;
  min-width: 0;
  flex: 1;
}
.folder-dialog__title {
  font-weight: var(--weight-semibold, 600);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.folder-dialog__meta {
  font-size: var(--text-caption1, 12px);
  color: var(--color-label-secondary);
}
.folder-dialog__empty {
  margin: 0;
  color: var(--color-label-secondary);
  font-size: var(--text-subhead, 14px);
}
.folder-dialog__create {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  padding-top: var(--space-2);
  border-top: 1px solid var(--color-separator);
}
.folder-dialog__input {
  flex: 1;
  min-width: 160px;
  padding: 6px 10px;
  border: 1px solid var(--color-separator);
  border-radius: var(--radius-sm, 8px);
  background: var(--color-bg-primary, transparent);
  color: var(--color-label);
}
.folder-dialog__public {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: var(--text-caption1, 12px);
  color: var(--color-label-secondary);
  cursor: pointer;
}
</style>
