<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router'
import { DialogPlugin } from '@/shared/ui/feedback'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { groupApi } from '../../infrastructure/groupApi'
import { groupPath, groupMembershipLabel, safeGroupURL, type Group, type GroupMember, type GroupMessage } from '../../domain/group'
import { useGroups } from '../composables/useGroups'
import { useGroupJoin } from '../composables/useGroupJoin'
import GroupAvatar from '../components/GroupAvatar.vue'
import GroupMessageView from '../components/GroupMessageView.vue'
import GroupComposer from '../components/GroupComposer.vue'
import GroupDetails from '../components/GroupDetails.vue'
import GroupMemberInput from '../components/GroupMemberInput.vue'
import EmojiPicker from '@/shared/components/post/EmojiPicker.vue'
import SearchBox from '@/shared/components/widgets/SearchBox.vue'
import type { MastodonEmoji } from '@/shared/types/mastodon'
import { isMobileLayout, scrollDocumentToEnd, useDockedComposer } from '@/shared/composables/useMobileDock'
import { t } from '@/shared/i18n'
const route = useRoute(), router = useRouter()
const selectedId = computed(() => typeof route.params.groupId === 'string' ? route.params.groupId : '')
const { groups, group, messages, listCursor, historyCursor, loading, listLoading, historyLoading, error, listError, syncError, syncPaused, refreshing, refreshAfterPause, canSend, loadGroups, openRoom, loadHistory, upsertGroup, mergeMessages } = useGroups(selectedId)
const panel = ref<'create' | null>(null), actionError = ref(''), mutationBusy = ref(false), status = ref('')
const createName = ref(''), createTopic = ref(''), createInvite = ref(''), visibility = ref<'private' | 'public'>('private'), consent = ref(false)
const externalClientURL = ref('')
const members = ref<GroupMember[]>([]), membersCursor = ref<string | null>(null), membersLoading = ref(false), membersError = ref('')
const composerLocked = ref(false)
const reply = ref<GroupMessage | null>(null), editing = ref<GroupMessage | null>(null), dirty = ref(false), detailsOpen = ref(false)
const detailsDialog = ref<HTMLDialogElement>(), reactionDialog = ref<HTMLDialogElement>(), reactionMessage = ref<GroupMessage | null>(null)
const composerDock = ref<HTMLElement>()
useDockedComposer(composerDock)
const timeline = ref<HTMLElement>(), atBottom = ref(true), unseen = ref(0)
const clientURL = computed(() => safeGroupURL(group.value?.client_url))
const replyById = computed(() => new Map(messages.value.map(message => [message.id, message])))
const typingNames = computed(() => group.value?.typing_users?.filter(person => person.id !== group.value?.current_user_id).map(person => person.display_name || person.id).join('、') || '')
let active = true, actionVersion = 0
let lastRead = '', createTxn = crypto.randomUUID(), createSignature = ''
const { busy: joinBusy, error: joinError, status: joinStatus, operation: joinOperation, join: acceptJoin } = useGroupJoin(selectedId, result => {
  if (result.client_required) { externalClientURL.value = safeGroupURL(result.client_url); status.value = t('group.GroupsPage.s74') }
  if (result.group) { upsertGroup(result.group); void openRoom() }
})
const actionBusy = computed(() => mutationBusy.value || joinBusy.value)
const isActionCurrent = (version: number, epoch: number) => active && version === actionVersion && epoch === sessionEpoch.value
function confirmAction(header: string, body: string, confirm = t('group.GroupsPage.s75')): Promise<boolean> {
  const reopenDetails = detailsOpen.value
  const epoch = sessionEpoch.value, version = actionVersion
  if (reopenDetails) { detailsDialog.value?.close(); detailsOpen.value = false }
  return new Promise(resolve => {
    const dialog = DialogPlugin.confirm({ header, body, confirmBtn: confirm, cancelBtn: t('group.GroupsPage.s76'), onConfirm: () => { dialog.destroy(); resolve(isActionCurrent(version, epoch)) }, onClose: () => { dialog.destroy(); if (reopenDetails && isActionCurrent(version, epoch)) detailsOpen.value = true; resolve(false) } })
  })
}
async function protectDraft() { return !dirty.value || await confirmAction(t('group.GroupsPage.s77'), t('group.GroupsPage.s78'), t('group.GroupsPage.s79')) }
async function openPanel() { if (!await protectDraft()) return; dirty.value = false; panel.value = panel.value ? null : 'create'; actionError.value = '' }
onBeforeRouteLeave(protectDraft)
onBeforeRouteUpdate(async to => to.params.groupId === route.params.groupId || await protectDraft())
watch(selectedId, () => { actionVersion++; mutationBusy.value = false; reply.value = null; editing.value = null; dirty.value = false; composerLocked.value = false; detailsOpen.value = false; members.value = []; membersCursor.value = null; membersLoading.value = false; membersError.value = ''; actionError.value = ''; status.value = ''; lastRead = ''; atBottom.value = true; unseen.value = 0; panel.value = null }, { flush: 'sync' })
watch(() => route.query.action, value => {
  if (value !== 'create' || selectedId.value) return
  panel.value = 'create'
  actionError.value = ''
  // Consume the shortcut so closing and opening it again always works.
  void router.replace({ query: { ...route.query, action: undefined } }).catch(() => {})
}, { immediate: true })
// Previously shared join links now enter the one search/preview flow.
watch(() => route.query.join, value => {
  if (typeof value !== 'string') return
  void router.replace({ name: 'search', query: { q: value.slice(0, 2000), type: 'groups', via: route.query.via, preview: route.query.preview } }).catch(() => {})
}, { immediate: true })
watch(detailsOpen, async open => { await nextTick(); if (open) { detailsDialog.value?.showModal(); void loadMembers() } else detailsDialog.value?.close() })
watch(() => group.value?.membership, value => { if (value === 'join') void loadMembers() })
watch(() => group.value?.encryption, value => { if (value !== 'unencrypted') { reactionMessage.value = null; reactionDialog.value?.close() } })
watch(() => messages.value.length, async (length, old) => { if (length > old && !historyLoading.value) { if (atBottom.value) await scrollBottom(); else unseen.value += length - old } })
watch(loading, async value => { if (!value) await scrollBottom() })
watch(sessionEpoch, () => { actionVersion++; reply.value = null; editing.value = null; dirty.value = false; members.value = []; membersCursor.value = null; membersLoading.value = false; membersError.value = ''; detailsOpen.value = false; panel.value = null; actionError.value = ''; status.value = ''; mutationBusy.value = false }, { flush: 'sync' })
async function scrollBottom() { await nextTick(); if (isMobileLayout()) scrollDocumentToEnd(); else if (timeline.value) timeline.value.scrollTop = timeline.value.scrollHeight; atBottom.value = true; unseen.value = 0; void markRead() }
function setAtBottom(value: boolean) { atBottom.value = value; if (value) { unseen.value = 0; void markRead() } }
// Desktop: the timeline element scrolls; read its geometry once per frame.
let scrollFrame = 0
function onScroll() { if (isMobileLayout() || scrollFrame) return; scrollFrame = requestAnimationFrame(() => { scrollFrame = 0; const el = timeline.value; if (el) setAtBottom(el.scrollHeight - el.scrollTop - el.clientHeight < 80) }) }
async function markRead() {
  const latest = messages.value.at(-1), id = selectedId.value, epoch = sessionEpoch.value, version = actionVersion
  if (!latest || document.visibilityState !== 'visible' || !atBottom.value || group.value?.membership !== 'join' || lastRead === latest.id) return
  lastRead = latest.id
  try { await groupApi.read(id, latest.id); if (isActionCurrent(version, epoch) && group.value?.id === id) upsertGroup({ ...group.value, unread_count: 0 }) }
  catch { if (isActionCurrent(version, epoch) && lastRead === latest.id) lastRead = '' }
}
async function older() {
  const el = timeline.value, mobile = isMobileLayout(), doc = document.scrollingElement ?? document.documentElement
  const height = mobile ? doc.scrollHeight : el?.scrollHeight ?? 0
  await loadHistory(); await nextTick()
  if (mobile) window.scrollBy(0, doc.scrollHeight - height)
  else if (el) el.scrollTop += el.scrollHeight - height
}
// Phones scroll the document: an IntersectionObserver on a sentinel after the
// timeline says whether the end is in view, so no scroll listener reads layout.
const endSentinel = ref<HTMLElement>()
let endObserver: IntersectionObserver | undefined
watch(endSentinel, el => {
  endObserver?.disconnect(); endObserver = undefined
  if (!el || typeof IntersectionObserver === 'undefined') return
  endObserver = new IntersectionObserver(entries => { if (isMobileLayout()) setAtBottom(entries.some(entry => entry.isIntersecting)) }, { rootMargin: '0px 0px 80px 0px' })
  endObserver.observe(el)
})
// Keep the newest message visible when the keyboard opens or closes. Toolbar
// collapse during a swipe also resizes the viewport; ignore it unless typing in the composer.
let resizeFrame = 0
function onViewportResize() {
  if (resizeFrame || !isMobileLayout() || !composerDock.value?.contains(document.activeElement)) return
  resizeFrame = requestAnimationFrame(() => { resizeFrame = 0; if (atBottom.value && timeline.value) scrollDocumentToEnd() })
}
async function loadMembers(more = false) {
  const id = selectedId.value, epoch = sessionEpoch.value, version = actionVersion
  if (!id || group.value?.membership !== 'join' || membersLoading.value || (more && !membersCursor.value)) return
  membersLoading.value = true; membersError.value = ''
  try { const page = await groupApi.members(id, more ? membersCursor.value! : undefined); if (!isActionCurrent(version, epoch)) return; members.value = more ? [...new Map([...members.value, ...page.members].map(m => [m.id, m])).values()] : page.members; membersCursor.value = page.next_cursor }
  catch (e) { if (isActionCurrent(version, epoch)) membersError.value = e instanceof Error ? e.message : t('group.GroupsPage.s80') }
  finally { if (isActionCurrent(version, epoch)) membersLoading.value = false }
}
async function create() {
  if (actionBusy.value || !consent.value || !createName.value.trim()) return
  const input = { name: createName.value.trim(), topic: createTopic.value, visibility: visibility.value, invite: createInvite.value.split(/[\s,，]+/).filter(Boolean), plaintext_consent: true as const }
  const signature = JSON.stringify(input)
  if (signature !== createSignature) { createTxn = crypto.randomUUID(); createSignature = signature }
  const epoch = sessionEpoch.value, version = actionVersion
  mutationBusy.value = true; actionError.value = ''
  try { const created = await groupApi.create({ ...input, transaction_id: createTxn }); if (!isActionCurrent(version, epoch)) return; upsertGroup(created); panel.value = null; createName.value = ''; createTopic.value = ''; createInvite.value = ''; consent.value = false; await router.push(groupPath(created.id)); if (active && epoch === sessionEpoch.value && selectedId.value === created.id && created.invite_failures?.length) actionError.value = t('group.GroupsPage.s81', [created.invite_failures.map(f => f.user_id + '（' + f.error + '）').join('、')]) }
  catch (e) { if (isActionCurrent(version, epoch)) actionError.value = e instanceof Error ? e.message : t('group.GroupsPage.s82') }
  finally { if (isActionCurrent(version, epoch)) mutationBusy.value = false }
}
async function join(item: Group) { if (!actionBusy.value) { actionError.value = ''; await acceptJoin(item) } }
async function leave() {
  const current = group.value
  if (!current || actionBusy.value) return
  const epoch = sessionEpoch.value, version = actionVersion
  const verb = current.membership === 'invite' ? t('group.GroupsPage.s50') : current.membership === 'knock' ? t('group.GroupsPage.s51') : t('group.GroupsPage.s83')
  if (!await confirmAction(`${verb}？`, t('group.GroupsPage.s84'), verb)) return
  if (!isActionCurrent(version, epoch) || actionBusy.value) return
  mutationBusy.value = true; actionError.value = ''
  status.value = t('group.GroupsPage.s85', [verb])
  try { await groupApi.leave(current.id); if (!isActionCurrent(version, epoch)) return; dirty.value = false; detailsOpen.value = false; upsertGroup({ ...current, membership: 'leave' }); await router.push('/groups'); if (active && epoch === sessionEpoch.value) void loadGroups() }
  catch (e) { if (isActionCurrent(version, epoch)) { status.value = ''; actionError.value = e instanceof Error ? e.message : t('group.GroupsPage.s86') } }
  finally { if (isActionCurrent(version, epoch)) mutationBusy.value = false }
}
async function remove(message: GroupMessage) {
  const id = selectedId.value, epoch = sessionEpoch.value, version = actionVersion
  if (actionBusy.value || !await confirmAction(t('group.GroupsPage.s87'), t('group.GroupsPage.s88'), t('group.GroupsPage.s89'))) return
  if (!isActionCurrent(version, epoch) || actionBusy.value) return
  mutationBusy.value = true; actionError.value = ''; status.value = t('group.GroupsPage.s90')
  try { await groupApi.remove(id, message.id); if (isActionCurrent(version, epoch)) { mergeMessages([{ ...message, redacted: true, body: '', attachments: [], reactions: [] }]); status.value = t('group.GroupsPage.s91') } }
  catch (e) { if (isActionCurrent(version, epoch)) { status.value = ''; actionError.value = e instanceof Error ? e.message : t('group.GroupsPage.s92') } }
  finally { if (isActionCurrent(version, epoch)) mutationBusy.value = false }
}
async function react(message: GroupMessage, key: string, own: boolean) {
  if (!canSend.value || actionBusy.value) return
  const id = selectedId.value, epoch = sessionEpoch.value, version = actionVersion; mutationBusy.value = true; actionError.value = ''; status.value = t('group.GroupsPage.s93')
  try { await groupApi.reaction(id, message.id, key, own); if (isActionCurrent(version, epoch)) { reactionDialog.value?.close(); reactionMessage.value = null; status.value = t('group.GroupsPage.s94') } }
  catch (e) { if (isActionCurrent(version, epoch)) { status.value = ''; actionError.value = e instanceof Error ? e.message : t('group.GroupsPage.s95') } }
  finally { if (isActionCurrent(version, epoch)) mutationBusy.value = false }
}
function reactionPicker(message: GroupMessage) { reactionMessage.value = message; reactionDialog.value?.showModal() }
function customReaction(emoji: MastodonEmoji) { if (emoji.local_only) { actionError.value = t('group.GroupsPage.s96'); return } if (reactionMessage.value) void react(reactionMessage.value, `:${emoji.shortcode}:`, false) }
function receivedSent(incoming: GroupMessage[]) { mergeMessages(incoming); status.value = t('group.GroupsPage.s97'); void scrollBottom() }
async function context(message: GroupMessage, edit = false) { if (composerLocked.value) return; if (edit && dirty.value && !await confirmAction(t('group.GroupsPage.s98'), t('group.GroupsPage.s99'), t('group.GroupsPage.s100'))) return; if (edit) { editing.value = message; reply.value = null } else { reply.value = message; editing.value = null } }
onMounted(() => { document.addEventListener('visibilitychange', markRead); window.visualViewport?.addEventListener('resize', onViewportResize, { passive: true }) })
onBeforeUnmount(() => { active = false; actionVersion++; document.removeEventListener('visibilitychange', markRead); endObserver?.disconnect(); if (scrollFrame) cancelAnimationFrame(scrollFrame); if (resizeFrame) cancelAnimationFrame(resizeFrame); window.visualViewport?.removeEventListener('resize', onViewportResize) })
</script>
<template>
  <div class="groups-page" :class="{ 'groups-page--selected': !!selectedId, 'groups-page--panel': !!panel }">
    <aside class="groups-list" :aria-label="$t('group.GroupsPage.s1')">
      <header class="groups-list__header"><h1>{{ $t('group.GroupsPage.s2') }}</h1></header>
      <div class="groups-list__tools"><button type="button" class="btn btn--primary btn--sm" @click="openPanel">{{ $t('group.GroupsPage.s3') }}</button></div>
      <div class="groups-list__search-field"><SearchBox embedded search-type="groups" allow-empty :placeholder="$t('group.GroupsPage.s4')" /></div>
      <p v-if="syncPaused && !selectedId" class="groups-notice" role="status">{{ syncError }} <button type="button" :disabled="refreshing" @click="refreshAfterPause">{{ refreshing ? $t('group.GroupsPage.s5') : $t('group.GroupsPage.s6') }}</button></p>
      <p v-if="listError" class="groups-notice groups-notice--error" role="alert">{{ listError }} <button type="button" @click="loadGroups()">{{ $t('group.GroupsPage.s7') }}</button></p>
      <div class="groups-list__items">
<RouterLink v-for="item in groups" :key="item.id" :to="groupPath(item.id)" class="group-row" :class="{ 'group-row--active': item.id === selectedId }" :aria-current="item.id === selectedId ? 'page' : undefined"><GroupAvatar :name="item.name || $t('group.GroupsPage.s2')" :url="item.avatar_url" /><span class="group-row__text"><strong>{{ item.name || $t('group.GroupsPage.s8') }}</strong><small>{{ item.membership !== 'join' ? groupMembershipLabel[item.membership] : item.muted ? $t('group.GroupsPage.s9') : item.encryption === 'encrypted' ? $t('group.GroupsPage.s10') : $t('group.GroupsPage.s12', [item.member_count ?? $t('group.GroupsPage.s11')]) }}</small></span><span v-if="item.unread_count" class="group-row__badge" :aria-label="$t('group.GroupsPage.s13', [item.unread_count])">{{ item.unread_count > 99 ? '99+' : item.unread_count }}</span></RouterLink>
        <p v-if="listLoading" class="groups-list__empty" role="status">{{ $t('group.GroupsPage.s14') }}</p><p v-else-if="!groups.length && !listError" class="groups-list__empty">{{ $t('group.GroupsPage.s15') }}</p><button v-if="listCursor" class="btn btn--ghost btn--sm" type="button" :disabled="listLoading" @click="loadGroups(true)">{{ $t('group.GroupsPage.s16') }}</button>
</div>
    </aside>
    <section class="groups-main" :aria-label="$t('group.GroupsPage.s17')">
      <div v-if="panel" class="groups-setup">
<header><div><h2>{{ $t('group.GroupsPage.s3') }}</h2><p class="groups-setup__intro">{{ $t('group.GroupsPage.s18') }}</p></div><button type="button" :aria-label="$t('group.GroupsPage.s19')" :disabled="actionBusy" @click="panel = null">×</button></header>
        <p v-if="actionError" class="groups-notice groups-notice--error" role="alert">{{ actionError }}</p>
        <form class="group-setup-form" @submit.prevent="create"><label>{{ $t('group.GroupsPage.s20') }}<input v-model="createName" required maxlength="255" :placeholder="$t('group.GroupsPage.s21')" :disabled="actionBusy" /></label><label>{{ $t('group.GroupsPage.s22') }}<textarea v-model="createTopic" maxlength="512" rows="3" :placeholder="$t('group.GroupsPage.s23')" :disabled="actionBusy" /></label><fieldset><legend>{{ $t('group.GroupsPage.s24') }}</legend><label class="group-setup-choice"><input v-model="visibility" type="radio" value="private" :disabled="actionBusy" /><span>{{ $t('group.GroupsPage.s25') }}<small>{{ $t('group.GroupsPage.s26') }}</small></span></label><label class="group-setup-choice"><input v-model="visibility" type="radio" value="public" :disabled="actionBusy" /><span>{{ $t('group.GroupsPage.s27') }}<small>{{ $t('group.GroupsPage.s28') }}</small></span></label></fieldset><GroupMemberInput v-model="createInvite" :label="$t('group.GroupsPage.s29')" multiple :disabled="actionBusy" /><label class="group-setup-choice group-setup-consent"><input v-model="consent" type="checkbox" :disabled="actionBusy" /><span>{{ $t('group.GroupsPage.s30') }}<small>{{ $t('group.GroupsPage.s31') }}</small></span></label><button class="btn btn--primary" :disabled="actionBusy || !consent || !createName.trim()">{{ actionBusy ? $t('group.GroupsPage.s32') : $t('group.GroupsPage.s3') }}</button></form>
        <p v-if="status" class="groups-notice" role="status">{{ status }}</p><a v-if="externalClientURL" class="btn btn--secondary btn--sm" :href="externalClientURL" target="_blank" rel="noopener noreferrer">{{ $t('group.GroupsPage.s33') }}</a>
      </div>
      <template v-else-if="selectedId">
        <header class="group-thread-header"><RouterLink to="/groups" class="group-thread-header__back" :aria-label="$t('group.GroupsPage.s34')"><t-icon name="chevron-left" :size="24" /></RouterLink><GroupAvatar :name="group?.name || $t('group.GroupsPage.s2')" :url="group?.avatar_url" :size="36" /><div><h2>{{ group?.name || (loading ? $t('group.GroupsPage.s35') : $t('group.GroupsPage.s36')) }}</h2><p>{{ group ? $t('group.GroupsPage.s37', [groupMembershipLabel[group.membership], group.member_count ?? $t('group.GroupsPage.s11')]) : '' }}</p></div><button v-if="group" class="btn btn--ghost btn--sm" type="button" :aria-expanded="detailsOpen" @click="detailsOpen = true">{{ $t('group.GroupsPage.s38') }}</button></header>
        <p v-if="syncError" class="groups-notice" role="status">{{ syncError }} <template v-if="syncPaused"><button type="button" :disabled="refreshing" @click="refreshAfterPause">{{ refreshing ? $t('group.GroupsPage.s5') : $t('group.GroupsPage.s6') }}</button><a v-if="clientURL" :href="clientURL" target="_blank" rel="noopener noreferrer">{{ $t('group.GroupsPage.s39') }}</a></template></p><p v-if="error || actionError || joinError" class="groups-notice groups-notice--error" role="alert">{{ error || actionError || joinError }} <button v-if="error" type="button" @click="openRoom">{{ $t('group.GroupsPage.s40') }}</button></p>
        <div v-if="loading" class="groups-placeholder" role="status"><t-loading /><p>{{ $t('group.GroupsPage.s41') }}</p></div>
        <template v-else-if="group">
          <div v-if="group.membership !== 'join'" class="groups-state"><h3>{{ groupMembershipLabel[group.membership] }}</h3><p>{{ group.membership === 'invite' ? $t('group.GroupsPage.s42') : group.membership === 'knock' ? $t('group.GroupsPage.s43') : group.membership === 'ban' ? $t('group.GroupsPage.s44') : $t('group.GroupsPage.s45') }}</p><div><button v-if="['invite','leave','none'].includes(group.membership)" class="btn btn--primary btn--sm" type="button" :disabled="actionBusy" @click="join(group)">{{ joinBusy ? $t('group.GroupsPage.s46') : joinOperation ? $t('group.GroupsPage.s47') : group.membership === 'invite' ? $t('group.GroupsPage.s48') : $t('group.GroupsPage.s49') }}</button><button v-if="['invite','knock'].includes(group.membership)" class="btn btn--secondary btn--sm" type="button" :disabled="actionBusy" @click="leave">{{ group.membership === 'invite' ? $t('group.GroupsPage.s50') : $t('group.GroupsPage.s51') }}</button></div></div>
          <div v-else-if="group.room_type === 'm.space'" class="groups-state"><h3>{{ $t('group.GroupsPage.s52') }}</h3><p>{{ $t('group.GroupsPage.s53') }}</p><a v-if="clientURL" class="btn btn--primary btn--sm" :href="clientURL" target="_blank" rel="noopener noreferrer">{{ $t('group.GroupsPage.s39') }}</a></div>
          <div v-else-if="group.encryption !== 'unencrypted'" class="groups-state"><t-icon name="lock-on" :size="36" /><h3>{{ group.encryption === 'encrypted' ? $t('group.GroupsPage.s54') : $t('group.GroupsPage.s55') }}</h3><p>{{ group.encryption === 'encrypted' ? $t('group.GroupsPage.s56') : $t('group.GroupsPage.s57') }}</p><a v-if="clientURL" class="btn btn--primary btn--sm" :href="clientURL" target="_blank" rel="noopener noreferrer">{{ $t('group.GroupsPage.s33') }}</a><button v-if="group.encryption === 'unknown'" class="btn btn--secondary btn--sm" type="button" @click="openRoom">{{ $t('group.GroupsPage.s58') }}</button></div>
          <template v-else>
            <div v-if="group.replacement_id" class="groups-notice">{{ $t('group.GroupsPage.s59') }}<RouterLink :to="{ name: 'search', query: { q: group.replacement_id, type: 'groups', via: group.via } }">{{ $t('group.GroupsPage.s60') }}</RouterLink></div>
            <div ref="timeline" class="group-timeline" role="log" :aria-label="$t('group.GroupsPage.s61')" aria-live="off" @scroll.passive="onScroll"><button v-if="historyCursor" class="group-history" type="button" :disabled="historyLoading" @click="older">{{ historyLoading ? $t('group.GroupsPage.s62') : $t('group.GroupsPage.s63') }}</button><div v-if="!messages.length" class="groups-placeholder"><t-icon name="chat" :size="32" /><h3>{{ $t('group.GroupsPage.s64') }}</h3><p>{{ $t('group.GroupsPage.s65') }}</p></div><GroupMessageView v-for="message in messages" :key="`${group.id}:${message.id}`" :message="message" :group-topic="group.topic" :reply="message.reply_to ? replyById.get(message.reply_to) : undefined" :can-send="canSend" :can-redact="group.capabilities.redact" :busy="actionBusy || composerLocked" @reply="context(message)" @edit="context(message, true)" @remove="remove(message)" @reaction="(key, own) => react(message, key, own)" @picker="reactionPicker(message)" /></div>
            <div ref="endSentinel" class="group-timeline__end" aria-hidden="true"></div>
            <button v-if="unseen" class="group-new-messages" type="button" @click="scrollBottom">{{ $t('group.GroupsPage.s66', [unseen]) }}</button>
          </template>
          <div ref="composerDock" class="group-composer-dock"><p v-if="typingNames && canSend" class="group-typing" role="status">{{ $t('group.GroupsPage.s67', [typingNames]) }}</p>
          <GroupComposer v-if="group.membership === 'join'" :key="group.id" :group="group" :reply="reply" :editing="editing" :members="members" @sent="receivedSent" @cancel-context="reply = null; editing = null" @dirty="dirty = $event" @locked="composerLocked = $event" /></div>
          <p v-if="actionBusy" class="groups-notice" role="status">{{ joinBusy ? joinStatus : status }}</p><p class="groups-announcer" role="status">{{ status || joinStatus }}</p>
        </template>
      </template>
      <div v-else class="groups-welcome"><span class="groups-welcome__icon"><t-icon name="usergroup" :size="38" /></span><h2>{{ $t('group.GroupsPage.s68') }}</h2><p>{{ $t('group.GroupsPage.s69') }}<br />{{ $t('group.GroupsPage.s70') }}</p><button class="btn btn--primary" type="button" :disabled="actionBusy" @click="openPanel">{{ $t('group.GroupsPage.s3') }}</button><p class="groups-subtle">{{ $t('group.GroupsPage.s71') }}</p></div>
    </section>
    <dialog ref="detailsDialog" class="group-details-dialog" :aria-label="$t('group.GroupsPage.s38')" @cancel="detailsOpen = false" @click="($event.target === detailsDialog) && (detailsOpen = false)"><GroupDetails v-if="group && detailsOpen" :group="group" :members="members" :members-cursor="membersCursor" :members-loading="membersLoading" :members-error="membersError" :pending="actionBusy" @close="detailsOpen = false" @updated="upsertGroup" @reload-members="loadMembers()" @more-members="loadMembers(true)" @leave="leave" /></dialog>
    <dialog ref="reactionDialog" class="group-reaction-dialog" :aria-label="$t('group.GroupsPage.s72')" @cancel="reactionMessage = null"><header><h2>{{ $t('group.GroupsPage.s72') }}</h2><button type="button" :aria-label="$t('group.GroupsPage.s73')" @click="reactionDialog?.close(); reactionMessage = null">×</button></header><p v-if="actionError" class="groups-notice groups-notice--error" role="alert">{{ actionError }}</p><div class="group-reaction-unicode"><button v-for="emoji in ['👍','❤️','😂','🎉','🤔','😊','🙏','👀','😢','🚀','✅','✨']" :key="emoji" type="button" :disabled="actionBusy || !canSend" @click="reactionMessage && react(reactionMessage, emoji, false)">{{ emoji }}</button></div><EmojiPicker @select="customReaction" @close="reactionDialog?.close(); reactionMessage = null" /></dialog>
  </div>
</template>
<style scoped src="./groups.css"></style>
