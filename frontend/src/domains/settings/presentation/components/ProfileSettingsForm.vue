<script setup lang="ts">
import { ref, reactive, computed, onMounted, onBeforeUnmount, shallowRef, watch, nextTick, useId } from 'vue'
import { MessagePlugin } from '@/shared/ui/feedback'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { userApi } from '@/domains/user/infrastructure/userApi'
import { postApi } from '@/domains/post/infrastructure/postApi'
import { stripHtml } from '@/shared/utils/sanitize'
import { sameOriginMediaUrl } from '@/shared/utils/mediaUrl'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import ImageCropDialog from '@/shared/components/common/ImageCropDialog.vue'
import AvatarDecorationPicker from './AvatarDecorationPicker.vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { validateProfile, validateImageFile, MAX_DISPLAY_NAME_LENGTH, MAX_NOTE_LENGTH } from '../../domain/validation'

import { t } from '@/shared/i18n'
const authStore = useAuthStore()
const imageInputId = useId()
const decorationOpen = ref(false)
const savingDecoration = ref(false)
const decorationDialog = ref<HTMLDialogElement>()
const decorationTrigger = ref<HTMLButtonElement>()
watch(decorationOpen, visible => { if (visible) decorationDialog.value?.showModal() }, { flush: 'post' })
function openDecorations() { savingDecoration.value = false; decorationOpen.value = true }
function closeDecorations() {
  if (savingDecoration.value) return
  decorationDialog.value?.close()
  decorationOpen.value = false
  void nextTick(() => decorationTrigger.value?.focus({ preventScroll: true }))
}
function decorationsSaved() {
  savingDecoration.value = false
  closeDecorations()
  MessagePlugin.success(t('settings.AvatarDecorationPicker.s24'))
}

const profile = reactive({ display_name: '', note: '', locked: false, discoverable: false, indexable: false, avatar: '', header: '' })
const profileError = ref<string | null>(null)
const savingProfile = ref(false)
const uploading = reactive({ avatar: false, header: false })
type ImageKind = 'avatar' | 'header'
const cropRequest = shallowRef<{ kind: ImageKind; file: File; input: HTMLInputElement } | null>(null)
let alive = true
let imageEpoch = 0

// A completed upload belongs only to the account/session that selected the image.
watch([() => authStore.currentUser?.id, sessionEpoch], () => {
  imageEpoch++
  decorationDialog.value?.close()
  decorationOpen.value = false
  savingDecoration.value = false
  cropRequest.value = null
  uploading.avatar = false
  uploading.header = false
  savingProfile.value = false
  profileError.value = null
  if (authStore.currentUser) resetProfile()
  else Object.assign(profile, { display_name: '', note: '', locked: false, discoverable: false, indexable: false, avatar: '', header: '' })
}, { flush: 'sync' })
onBeforeUnmount(() => { alive = false; imageEpoch++; decorationDialog.value?.close() })

const previewAccount = computed(() => authStore.currentUser
  ? { ...authStore.currentUser, avatar: profile.avatar, avatar_static: profile.avatar }
  : null)

function resetProfile() {
  const me = authStore.currentUser
  if (!me) return
  profile.display_name = me.display_name ?? ''
  // Edit the plain-text source, not the rendered HTML note.
  profile.note = me.source?.note ?? stripHtml(me.note ?? '')
  profile.locked = !!me.locked
  profile.discoverable = !!me.discoverable
  profile.indexable = !!me.indexable
  profile.avatar = me.avatar ?? ''
  profile.header = me.header ?? ''
}

function onPickImage(kind: ImageKind, e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file || !authStore.currentUser || savingProfile.value || uploading.avatar || uploading.header) return
  const err = validateImageFile(file)
  if (err) {
    profileError.value = err
    return
  }
  profileError.value = null
  cropRequest.value = { kind, file, input }
}

async function applyImage(file: File) {
  const request = cropRequest.value
  if (!request || !authStore.currentUser) return
  const { kind, input } = request
  const epoch = ++imageEpoch
  const accountId = authStore.currentUser.id
  const session = sessionEpoch.value
  const isCurrent = () => alive && epoch === imageEpoch && accountId === authStore.currentUser?.id && session === sessionEpoch.value
  cropRequest.value = null
  uploading[kind] = true
  try {
    // update_credentials only accepts an image URL, so upload it as media first.
    const media = await postApi.uploadMedia(file)
    if (!isCurrent()) return
    if (!media.url) throw new Error(t('settings.ProfileSettingsForm.s13'))
    profile[kind] = media.url
  } catch (e2) {
    if (isCurrent()) profileError.value = e2 instanceof Error ? e2.message : t('settings.ProfileSettingsForm.s14')
  } finally {
    if (isCurrent()) {
      uploading[kind] = false
      await nextTick()
      if (input.isConnected && document.activeElement === document.body) input.focus({ preventScroll: true })
    }
  }
}

async function saveProfile() {
  if (savingProfile.value || uploading.avatar || uploading.header || cropRequest.value) return
  profileError.value = validateProfile(profile.display_name, profile.note)
  if (profileError.value) return
  const me = authStore.currentUser
  if (!me) return
  const session = sessionEpoch.value
  const isCurrent = () => alive && session === sessionEpoch.value && authStore.currentUser?.id === me.id
  savingProfile.value = true
  try {
    const updated = await userApi.updateCredentials({
      display_name: profile.display_name,
      note: profile.note,
      locked: profile.locked,
      discoverable: profile.discoverable,
      indexable: profile.indexable,
      ...(profile.avatar && profile.avatar !== me?.avatar ? { avatar: profile.avatar } : {}),
      ...(profile.header && profile.header !== me?.header ? { header: profile.header } : {}),
    })
    if (!isCurrent()) return
    authStore.currentUser = updated
    // update_credentials may omit `source`; refetch so the form and sidebar agree.
    await authStore.fetchCurrentUser()
    if (!isCurrent()) return
    resetProfile()
    MessagePlugin.success(t('settings.ProfileSettingsForm.s15'))
  } catch (e) {
    if (isCurrent()) profileError.value = e instanceof Error ? e.message : t('settings.ProfileSettingsForm.s16')
  } finally {
    if (isCurrent()) savingProfile.value = false
  }
}

onMounted(async () => {
  resetProfile()
  const initial = JSON.stringify(profile)
  const session = sessionEpoch.value
  // Make sure we have the latest `source` (plain-text bio) before editing.
  await authStore.fetchCurrentUser()
  // Do not overwrite edits made while the refresh was in flight.
  if (alive && session === sessionEpoch.value && JSON.stringify(profile) === initial) resetProfile()
})
</script>

<template>
  <form class="settings-form" @submit.prevent="saveProfile">
    <div class="settings-hero">
      <input :id="`${imageInputId}-avatar`" type="file" accept="image/jpeg,image/png,image/webp,image/gif,image/avif,image/bmp" class="sr-only settings-image-input" :disabled="savingProfile || uploading.avatar || uploading.header" @change="onPickImage('avatar', $event)" />
      <input :id="`${imageInputId}-header`" type="file" accept="image/jpeg,image/png,image/webp,image/gif,image/avif,image/bmp" class="sr-only settings-image-input" :disabled="savingProfile || uploading.avatar || uploading.header" @change="onPickImage('header', $event)" />
      <div class="settings-hero__cover">
        <img v-if="profile.header" :src="sameOriginMediaUrl(profile.header)" alt="" />
        <label :for="`${imageInputId}-header`" class="btn btn--secondary btn--sm settings-file settings-file--cover" :class="{ 'is-disabled': savingProfile || uploading.avatar || uploading.header }">
          <t-icon name="image" :size="18" aria-hidden="true" />
          {{ uploading.header ? $t('settings.ProfileSettingsForm.s1') : $t('settings.ProfileSettingsForm.s3') }}
        </label>
      </div>
      <div class="settings-hero__bar">
        <UserAvatar v-if="previewAccount" :account="previewAccount" :size="80" class="settings-avatar" />
        <div class="settings-hero__actions">
          <label :for="`${imageInputId}-avatar`" class="btn btn--secondary btn--sm settings-file settings-file--avatar" :class="{ 'is-disabled': savingProfile || uploading.avatar || uploading.header }">
            <t-icon name="image-add" :size="18" aria-hidden="true" />
            {{ uploading.avatar ? $t('settings.ProfileSettingsForm.s1') : $t('settings.ProfileSettingsForm.s2') }}
          </label>
          <button ref="decorationTrigger" type="button" class="btn btn--secondary btn--sm" :disabled="savingProfile || uploading.avatar || uploading.header" @click="openDecorations">
            <t-icon name="happy" :size="18" aria-hidden="true" />
            {{ $t('settings.SettingsPage.s12') }}
          </button>
        </div>
      </div>
    </div>

    <div class="settings-field">
      <label for="set-display-name" class="settings-field__label">{{ $t('settings.ProfileSettingsForm.s4') }}</label>
      <input id="set-display-name" v-model="profile.display_name" class="settings-input" type="text" :maxlength="MAX_DISPLAY_NAME_LENGTH * 2" autocomplete="nickname" />
    </div>
    <div class="settings-field">
      <label for="set-note" class="settings-field__label">{{ $t('settings.ProfileSettingsForm.s5') }}</label>
      <textarea id="set-note" v-model="profile.note" class="settings-input" rows="4" aria-describedby="set-note-hint" />
      <span id="set-note-hint" class="settings-field__hint">{{ Array.from(profile.note).length }} / {{ MAX_NOTE_LENGTH }}</span>
    </div>
    <div class="settings-profile-options">
    <label class="settings-check">
      <input v-model="profile.locked" type="checkbox" />
      <span>{{ $t('settings.ProfileSettingsForm.s6') }}</span>
    </label>
    <label class="settings-check">
      <input v-model="profile.discoverable" type="checkbox" />
      <span>{{ $t('settings.ProfileSettingsForm.s7') }}</span>
    </label>
    <label class="settings-check">
      <input v-model="profile.indexable" type="checkbox" />
      <span>{{ $t('settings.ProfileSettingsForm.s8') }}</span>
    </label>
    <p class="settings-field__hint">{{ $t('settings.ProfileSettingsForm.s9') }}</p>
    </div>

    <p v-if="profileError" class="settings-error" role="alert">{{ profileError }}</p>
    <div class="settings-actions">
      <button type="button" class="btn btn--ghost btn--sm" :disabled="savingProfile || uploading.avatar || uploading.header" @click="resetProfile">{{ $t('settings.ProfileSettingsForm.s10') }}</button>
      <button type="submit" class="btn btn--primary btn--sm" :disabled="savingProfile || uploading.avatar || uploading.header">
        {{ savingProfile ? $t('settings.ProfileSettingsForm.s11') : $t('settings.ProfileSettingsForm.s12') }}
      </button>
    </div>
  </form>
  <Teleport to="body">
    <dialog v-if="decorationOpen" ref="decorationDialog" class="settings-decoration-dialog" :aria-labelledby="`${imageInputId}-decoration-title`" @cancel.prevent="closeDecorations" @click.self="closeDecorations">
      <header class="settings-decoration-dialog__header">
        <h2 :id="`${imageInputId}-decoration-title`">{{ $t('settings.SettingsPage.s12') }}</h2>
        <button type="button" class="btn btn--ghost settings-decoration-dialog__close" autofocus :aria-label="$t('shared.ImageCropDialog.s1')" :disabled="savingDecoration" @click="closeDecorations"><t-icon name="close" :size="20" aria-hidden="true" /></button>
      </header>
      <div class="settings-decoration-dialog__body"><AvatarDecorationPicker :account="previewAccount ?? undefined" @busy-change="savingDecoration = $event" @saved="decorationsSaved" /></div>
    </dialog>
  </Teleport>
  <ImageCropDialog v-if="cropRequest" :file="cropRequest.file" :kind="cropRequest.kind" @cancel="cropRequest = null" @apply="applyImage" />
</template>

<style scoped>
.settings-decoration-dialog { width: min(640px, 94vw); max-width: none; max-height: calc(100dvh - 32px); margin: auto; padding: 0; border: 1px solid var(--color-separator); border-radius: 1rem; background: var(--color-bg-elevated); color: var(--color-label); box-shadow: var(--shadow-popover); }
.settings-decoration-dialog[open] { display: flex; flex-direction: column; }
.settings-decoration-dialog::backdrop { background: rgb(0 0 0 / 60%); }
.settings-decoration-dialog__header { flex: none; display: flex; align-items: center; justify-content: space-between; gap: 1rem; padding: .75rem 1.25rem; border-bottom: 1px solid var(--color-separator); }
.settings-decoration-dialog__header h2 { margin: 0; font-size: 1rem; font-weight: 600; }
.settings-decoration-dialog__close { display: flex; align-items: center; justify-content: center; width: 44px; min-height: 44px; padding: 0; }
.settings-decoration-dialog__body { min-height: 0; overflow-y: auto; overscroll-behavior: contain; padding: 1.25rem; }
.settings-decoration-dialog :is(button, input):focus-visible { outline: 2px solid var(--color-accent); outline-offset: 2px; }

.settings-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.settings-field {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.settings-field__label {
  font-size: var(--text-footnote, var(--font-sm));
  font-weight: 600;
}

.settings-field__hint {
  font-size: var(--text-footnote, var(--font-xs));
  color: var(--color-label-secondary, var(--text-secondary));
}

.settings-input {
  width: 100%;
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-separator, var(--border-color));
  border-radius: var(--radius-sm, 8px);
  background: transparent;
  color: inherit;
  font: inherit;
  resize: vertical;
}

.settings-actions {
  display: flex;
  gap: var(--space-2);
  justify-content: flex-end;
}

.settings-error {
  color: var(--color-danger, var(--accent-red, #e0245e));
  font-size: var(--font-sm);
}

.settings-hero {
  position: relative;
  border: 1px solid var(--color-separator);
  border-radius: 1rem;
  background: var(--color-bg-grouped);
}
.settings-hero__cover {
  position: relative;
  height: clamp(9rem, 16vw, 11.5rem);
  border-radius: 1rem 1rem 0 0;
  overflow: hidden;
  background: var(--color-fill);
}
.settings-hero__cover img { display: block; width: 100%; height: 100%; object-fit: cover; }
.settings-hero__bar { display: flex; flex-direction: column; align-items: center; gap: 1rem; padding: 0 1rem 1.25rem; }
.settings-avatar { flex: none; margin-top: -2.5rem; border-radius: 50%; box-shadow: 0 0 0 4px var(--color-bg-grouped); }
.settings-hero__actions { display: flex; flex-wrap: wrap; justify-content: center; gap: .5rem; }
.settings-hero__actions .btn, .settings-file--cover { display: inline-flex; align-items: center; justify-content: center; gap: .5rem; }
.settings-file { cursor: pointer; }
.settings-file.is-disabled { cursor: not-allowed; opacity: .5; }
.settings-file--cover { position: absolute; inset: 1rem 1rem auto auto; background: var(--color-bg-elevated); box-shadow: var(--shadow-1); }
.settings-hero:has(input[id$='-avatar']:focus-visible) .settings-file--avatar,
.settings-hero:has(input[id$='-header']:focus-visible) .settings-file--cover { outline: 2px solid var(--color-accent); outline-offset: 3px; }
.settings-profile-options { display: flex; flex-direction: column; gap: .25rem; padding: .75rem 1rem; background: var(--color-bg-grouped); border: 1px solid var(--color-separator); border-radius: .875rem; }
.settings-profile-options > p { margin: .5rem 0 0; }
@media (max-width: 499px) {
  .settings-file--cover { inset: .75rem .75rem auto auto; }
  .settings-hero__bar { padding-inline: .75rem; }
  .settings-hero__actions .btn { padding-inline: .75rem; }
}

.settings-check {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--font-sm);
}
</style>
