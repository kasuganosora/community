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
import ProfileFieldsEditor from './ProfileFieldsEditor.vue'
import ProfileQRCode from './ProfileQRCode.vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { validateProfile, validateImageFile, MAX_DISPLAY_NAME_LENGTH, MAX_NOTE_LENGTH } from '../../domain/validation'

import { t } from '@/shared/i18n'
import type { ReactionAcceptance } from '@/shared/types/mastodon'
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

type CollectionVisibility = 'public' | 'followers' | 'private'
const visibilityChoices: CollectionVisibility[] = ['public', 'followers', 'private']
const emptyProfile = () => ({ display_name: '', note: '', locked: false, discoverable: false, indexable: false, avatar: '', header: '', location: '', birthday: '', language: '', followed_message: '', reaction_acceptance: 'all' as ReactionAcceptance, is_cat: false, speak_as_cat: true, bot: false, require_signin_to_view: false, auto_accept_followed: false, hide_online_status: false, following_visibility: 'public' as CollectionVisibility, followers_visibility: 'public' as CollectionVisibility, fields: Array.from({ length: 4 }, () => ({ name: '', value: '' })) })
const profile = reactive(emptyProfile())
const fieldsReady = ref(false)
const refreshingSource = ref(false)
const languageChoices = ['ar', 'bg', 'bn', 'ca', 'cs', 'da', 'de', 'el', 'en', 'eo', 'es', 'et', 'eu', 'fa', 'fi', 'fr', 'ga', 'gl', 'he', 'hi', 'hr', 'hu', 'hy', 'id', 'is', 'it', 'ja', 'ka', 'ko', 'ku', 'lo', 'lt', 'lv', 'mk', 'ml', 'mn', 'ms', 'my', 'nb', 'nl', 'nn', 'pl', 'pt', 'pt-BR', 'ro', 'ru', 'sk', 'sl', 'sq', 'sr', 'sv', 'ta', 'te', 'th', 'tl', 'tr', 'uk', 'ur', 'uz', 'vi', 'zh-CN', 'zh-TW'].map(code => {
  let label = code
  try { label = new Intl.DisplayNames([code], { type: 'language' }).of(code) || code } catch { /* Keep the stored language selectable. */ }
  return [code, label] as const
})
const unlistedLanguage = computed(() => profile.language && !languageChoices.some(([code]) => code === profile.language))
const unlistedLanguageName = computed(() => {
  try { return new Intl.DisplayNames([document.documentElement.lang || 'en'], { type: 'language' }).of(profile.language) || profile.language }
  catch { return profile.language }
})
const reactionChoices = ['all', 'likeOnlyForRemote', 'nonSensitiveOnly', 'nonSensitiveOnlyForLocalLikeOnlyForRemote', 'likeOnly'] as const
const today = new Date()
const latestBirthday = `${today.getFullYear()}-${String(today.getMonth() + 1).padStart(2, '0')}-${String(today.getDate()).padStart(2, '0')}`
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
  else { Object.assign(profile, emptyProfile()); fieldsReady.value = false }
  refreshingSource.value = false
}, { flush: 'sync' })
onBeforeUnmount(() => { alive = false; imageEpoch++; decorationDialog.value?.close() })

const previewAccount = computed(() => authStore.currentUser
  ? { ...authStore.currentUser, avatar: profile.avatar, avatar_static: profile.avatar, is_cat: profile.is_cat }
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
  profile.location = me.location ?? ''
  profile.birthday = me.birthday ?? ''
  profile.language = me.language ?? me.source?.language ?? ''
  profile.followed_message = me.followed_message ?? ''
  profile.reaction_acceptance = me.reaction_acceptance ?? 'all'
  profile.is_cat = !!me.is_cat
  profile.bot = !!me.bot
  profile.require_signin_to_view = !!me.require_signin_to_view
  const src = me.source ?? { note: '' }
  profile.speak_as_cat = (src.speak_as_cat ?? me.speak_as_cat) !== false
  profile.auto_accept_followed = !!src.auto_accept_followed
  profile.hide_online_status = !!src.hide_online_status
  const fallback: CollectionVisibility = me.hide_collections ? 'private' : 'public'
  const vis = (v: unknown): CollectionVisibility => (visibilityChoices as unknown[]).includes(v) ? v as CollectionVisibility : fallback
  profile.following_visibility = vis(src.following_visibility ?? me.following_visibility)
  profile.followers_visibility = vis(src.followers_visibility ?? me.followers_visibility)
  resetFields()
}

function resetFields() {
  // Rendered fields contain HTML and must never be resubmitted as raw content.
  const raw = authStore.currentUser?.source?.fields
  fieldsReady.value = Array.isArray(raw)
  const fields = (raw ?? []).map(({ name, value }) => ({ name, value }))
  while (fields.length < 4) fields.push({ name: '', value: '' })
  profile.fields = fields
}

async function refreshProfileSource() {
  if (refreshingSource.value) return
  const initial = JSON.stringify(profile)
  const session = sessionEpoch.value
  const accountId = authStore.currentUser?.id
  const isCurrent = () => alive && session === sessionEpoch.value && accountId === authStore.currentUser?.id
  refreshingSource.value = true
  try {
    await authStore.fetchCurrentUser()
    if (!isCurrent()) return
    // Preserve drafts, including fields reordered while this request was pending.
    if (JSON.stringify(profile) === initial) resetProfile()
    else if (!fieldsReady.value) resetFields()
  } catch {
    // The auth store reports refresh errors; never replace a draft with HTML.
  } finally {
    if (isCurrent()) refreshingSource.value = false
  }
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
      location: profile.location,
      birthday: profile.birthday,
      language: profile.language.trim(),
      followed_message: profile.followed_message,
      reaction_acceptance: profile.reaction_acceptance,
      is_cat: profile.is_cat,
      bot: profile.bot,
      require_signin_to_view: profile.require_signin_to_view,
      speak_as_cat: profile.speak_as_cat,
      auto_accept_followed: profile.auto_accept_followed,
      hide_online_status: profile.hide_online_status,
      following_visibility: profile.following_visibility,
      followers_visibility: profile.followers_visibility,
      ...(fieldsReady.value ? { fields_attributes: profile.fields.filter(field => field.name.trim() || field.value.trim()).map(({ name, value }) => ({ name, value })) } : {}),
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

onMounted(() => { resetProfile(); void refreshProfileSource() })

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
      <input id="set-display-name" v-model="profile.display_name" class="settings-input" type="text" :maxlength="MAX_DISPLAY_NAME_LENGTH * 2" autocomplete="nickname" :disabled="savingProfile" />
    </div>
    <div class="settings-field">
      <label for="set-note" class="settings-field__label">{{ $t('settings.ProfileSettingsForm.s5') }}</label>
      <textarea id="set-note" v-model="profile.note" class="settings-input" rows="4" aria-describedby="set-note-hint" :disabled="savingProfile" />
      <span id="set-note-hint" class="settings-field__hint">{{ Array.from(profile.note).length }} / {{ MAX_NOTE_LENGTH }}</span>
    </div>
    <div class="settings-profile-grid">
      <div class="settings-field">
        <label for="set-location" class="settings-field__label">{{ $t('settings.ProfileExtras.location') }}</label>
        <input id="set-location" v-model="profile.location" class="settings-input" type="text" maxlength="128" autocomplete="off" aria-describedby="set-public-info-hint" :disabled="savingProfile" />
      </div>
      <div class="settings-field">
        <label for="set-birthday" class="settings-field__label">{{ $t('settings.ProfileExtras.birthday') }}</label>
        <input id="set-birthday" v-model="profile.birthday" class="settings-input" type="date" :max="latestBirthday" aria-describedby="set-public-info-hint" :disabled="savingProfile" />
      </div>
      <p id="set-public-info-hint" class="settings-field__hint settings-profile-grid__hint">{{ $t('settings.ProfileExtras.publicInfoHint') }}</p>
    </div>
    <div class="settings-field">
      <label for="set-profile-language" class="settings-field__label">{{ $t('settings.ProfileExtras.language') }}</label>
      <select id="set-profile-language" v-model="profile.language" class="settings-input" aria-describedby="set-language-hint" :disabled="savingProfile">
        <option value="">{{ $t('settings.ProfileExtras.autoLanguage') }}</option>
        <option v-if="unlistedLanguage" :value="profile.language">{{ unlistedLanguageName }}</option>
        <option v-for="[code, name] in languageChoices" :key="code" :value="code">{{ name }}</option>
      </select>
      <span id="set-language-hint" class="settings-field__hint">{{ $t('settings.ProfileExtras.languageHint') }}</span>
    </div>
    <div>
      <ProfileFieldsEditor v-model="profile.fields" :disabled="savingProfile || !fieldsReady" />
      <p v-if="!fieldsReady" class="settings-field__hint settings-fields-unavailable">{{ $t(refreshingSource ? 'settings.ProfileExtras.fieldsLoading' : 'settings.ProfileExtras.fieldsUnavailable') }} <button v-if="!refreshingSource" type="button" class="btn btn--ghost btn--sm" @click="refreshProfileSource">{{ $t('settings.ProfileExtras.retry') }}</button></p>
    </div>
    <div class="settings-field">
      <label for="set-followed-message" class="settings-field__label">{{ $t('settings.ProfileExtras.followedMessage') }}</label>
      <textarea id="set-followed-message" v-model="profile.followed_message" class="settings-input" rows="2" maxlength="200" aria-describedby="set-followed-message-hint" :disabled="savingProfile" />
      <span id="set-followed-message-hint" class="settings-field__hint">{{ $t('settings.ProfileExtras.followedMessageHint') }}</span>
    </div>
    <div class="settings-field">
      <label for="set-reaction-acceptance" class="settings-field__label">{{ $t('settings.ProfileExtras.reactionAcceptance') }}</label>
      <select id="set-reaction-acceptance" v-model="profile.reaction_acceptance" class="settings-input" :disabled="savingProfile"><option v-for="choice in reactionChoices" :key="choice" :value="choice">{{ $t(`settings.ProfileExtras.reaction.${choice}`) }}</option></select>
    </div>
    <div class="settings-profile-flags">
      <label class="settings-profile-flag" for="set-is-cat"><span><strong>{{ $t('settings.ProfileExtras.isCat') }}</strong><small>{{ $t('settings.ProfileExtras.isCatHint') }}</small></span><input id="set-is-cat" v-model="profile.is_cat" type="checkbox" role="switch" :disabled="savingProfile" /></label>
      <label v-if="profile.is_cat" class="settings-profile-flag" for="set-speak-as-cat" data-testid="speak-as-cat-toggle"><span><strong>{{ $t('settings.AccountPrivacy.speakAsCat') }}</strong><small>{{ $t('settings.AccountPrivacy.speakAsCatHint') }}</small></span><input id="set-speak-as-cat" v-model="profile.speak_as_cat" type="checkbox" role="switch" :disabled="savingProfile" /></label>
      <label class="settings-profile-flag" for="set-is-bot"><span><strong>{{ $t('settings.ProfileExtras.isBot') }}</strong><small>{{ $t('settings.ProfileExtras.isBotHint') }}</small></span><input id="set-is-bot" v-model="profile.bot" type="checkbox" role="switch" :disabled="savingProfile" /></label>
      <label class="settings-profile-flag" for="set-require-signin" data-testid="require-signin-toggle"><span><strong>{{ $t('settings.ProfileExtras.requireSignin') }}</strong><small>{{ $t('settings.ProfileExtras.requireSigninHint') }}</small></span><input id="set-require-signin" v-model="profile.require_signin_to_view" type="checkbox" role="switch" :disabled="savingProfile" /></label>
    </div>
    <div class="settings-profile-options">
    <label class="settings-check">
      <input v-model="profile.locked" type="checkbox" :disabled="savingProfile" />
      <span>{{ $t('settings.ProfileSettingsForm.s6') }}</span>
    </label>
    <label class="settings-check">
      <input v-model="profile.discoverable" type="checkbox" :disabled="savingProfile" />
      <span>{{ $t('settings.ProfileSettingsForm.s7') }}</span>
    </label>
    <label class="settings-check">
      <input v-model="profile.indexable" type="checkbox" :disabled="savingProfile" />
      <span>{{ $t('settings.ProfileSettingsForm.s8') }}</span>
    </label>
    <p class="settings-field__hint">{{ $t('settings.ProfileSettingsForm.s9') }}</p>
    </div>
    <section class="settings-account-privacy" data-testid="account-privacy" :aria-label="$t('settings.AccountPrivacy.title')">
      <h3 class="settings-field__label">{{ $t('settings.AccountPrivacy.title') }}</h3>
      <div class="settings-profile-flags">
        <label class="settings-profile-flag" for="set-auto-accept" data-testid="auto-accept-toggle"><span><strong>{{ $t('settings.AccountPrivacy.autoAcceptFollowed') }}</strong><small>{{ $t('settings.AccountPrivacy.autoAcceptFollowedHint') }}</small></span><input id="set-auto-accept" v-model="profile.auto_accept_followed" type="checkbox" role="switch" :disabled="savingProfile || !profile.locked" /></label>
        <label class="settings-profile-flag" for="set-hide-online" data-testid="hide-online-toggle"><span><strong>{{ $t('settings.AccountPrivacy.hideOnlineStatus') }}</strong><small>{{ $t('settings.AccountPrivacy.hideOnlineStatusHint') }}</small></span><input id="set-hide-online" v-model="profile.hide_online_status" type="checkbox" role="switch" :disabled="savingProfile" /></label>
      </div>
      <div class="settings-field">
        <label for="set-following-visibility" class="settings-field__label">{{ $t('settings.AccountPrivacy.followingVisibility') }}</label>
        <select id="set-following-visibility" v-model="profile.following_visibility" class="settings-input" :disabled="savingProfile"><option v-for="choice in visibilityChoices" :key="choice" :value="choice">{{ $t(`settings.AccountPrivacy.visibility.${choice}`) }}</option></select>
      </div>
      <div class="settings-field">
        <label for="set-followers-visibility" class="settings-field__label">{{ $t('settings.AccountPrivacy.followersVisibility') }}</label>
        <select id="set-followers-visibility" v-model="profile.followers_visibility" class="settings-input" :disabled="savingProfile"><option v-for="choice in visibilityChoices" :key="choice" :value="choice">{{ $t(`settings.AccountPrivacy.visibility.${choice}`) }}</option></select>
      </div>
    </section>

    <p v-if="profileError" class="settings-error" role="alert">{{ profileError }}</p>
    <div class="settings-actions">
      <button type="button" class="btn btn--ghost btn--sm" :disabled="savingProfile || uploading.avatar || uploading.header" @click="resetProfile">{{ $t('settings.ProfileSettingsForm.s10') }}</button>
      <button type="submit" class="btn btn--primary btn--sm" :disabled="savingProfile || uploading.avatar || uploading.header">
        {{ savingProfile ? $t('settings.ProfileSettingsForm.s11') : $t('settings.ProfileSettingsForm.s12') }}
      </button>
    </div>
  </form>
  <ProfileQRCode v-if="authStore.currentUser" :account="authStore.currentUser" />
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
.settings-profile-grid { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: .625rem 1rem; }
.settings-profile-grid__hint { grid-column: 1 / -1; margin: 0; }
.settings-fields-unavailable { display: flex; align-items: center; flex-wrap: wrap; gap: .5rem; margin: .5rem 0 0; }
.settings-profile-flags { display: flex; flex-direction: column; border: 1px solid var(--color-separator); border-radius: .875rem; background: var(--color-bg-grouped); }
.settings-profile-flag { display: flex; align-items: center; justify-content: space-between; gap: 1rem; padding: 1rem; cursor: pointer; }
.settings-profile-flag + .settings-profile-flag { border-top: 1px solid var(--color-separator); }
.settings-profile-flag span { display: flex; flex-direction: column; gap: .25rem; }
.settings-profile-flag strong { font-size: .875rem; font-weight: 600; }
.settings-profile-flag small { color: var(--color-label-secondary); line-height: 1.5; }
.settings-page .settings-profile-flag input { appearance: none; position: relative; width: 38px; height: 22px; flex: none; margin: 0; border: 1px solid var(--color-separator); border-radius: 999px; background: var(--color-fill); cursor: pointer; }
.settings-page .settings-profile-flag input::after { content: ''; position: absolute; top: 3px; left: 3px; width: 14px; height: 14px; border-radius: 50%; background: var(--color-label-secondary); transition: transform .15s ease; }
.settings-page .settings-profile-flag input:checked { background: var(--color-accent); border-color: var(--color-accent); }
.settings-page .settings-profile-flag input:checked::after { transform: translateX(16px); background: white; }
.settings-form :is(input, textarea, select):focus-visible { outline: 2px solid var(--color-accent); outline-offset: 2px; }
.settings-form :is(input, textarea, select):disabled { opacity: .55; cursor: not-allowed; }
@media (max-width: 499px) { .settings-profile-grid { grid-template-columns: minmax(0, 1fr); } }
@media (prefers-reduced-motion: reduce) { .settings-page .settings-profile-flag input::after { transition: none; } }
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
