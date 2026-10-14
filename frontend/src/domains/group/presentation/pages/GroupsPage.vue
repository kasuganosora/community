<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router'
import { DialogPlugin } from 'tdesign-vue-next'
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
const timeline = ref<HTMLElement>(), atBottom = ref(true), unseen = ref(0)
const clientURL = computed(() => safeGroupURL(group.value?.client_url))
const replyById = computed(() => new Map(messages.value.map(message => [message.id, message])))
const typingNames = computed(() => group.value?.typing_users?.filter(person => person.id !== group.value?.current_user_id).map(person => person.display_name || person.id).join('、') || '')
let active = true, actionVersion = 0
let lastRead = '', createTxn = crypto.randomUUID(), createSignature = ''
const { busy: joinBusy, error: joinError, status: joinStatus, operation: joinOperation, join: acceptJoin } = useGroupJoin(selectedId, result => {
  if (result.client_required) { externalClientURL.value = safeGroupURL(result.client_url); status.value = '已加入，请使用 Matrix 客户端继续操作。' }
  if (result.group) { upsertGroup(result.group); void openRoom() }
})
const actionBusy = computed(() => mutationBusy.value || joinBusy.value)
const isActionCurrent = (version: number, epoch: number) => active && version === actionVersion && epoch === sessionEpoch.value
function confirmAction(header: string, body: string, confirm = '继续'): Promise<boolean> {
  const reopenDetails = detailsOpen.value
  const epoch = sessionEpoch.value, version = actionVersion
  if (reopenDetails) { detailsDialog.value?.close(); detailsOpen.value = false }
  return new Promise(resolve => {
    const dialog = DialogPlugin.confirm({ header, body, confirmBtn: confirm, cancelBtn: '取消', onConfirm: () => { dialog.destroy(); resolve(isActionCurrent(version, epoch)) }, onClose: () => { dialog.destroy(); if (reopenDetails && isActionCurrent(version, epoch)) detailsOpen.value = true; resolve(false) } })
  })
}
async function protectDraft() { return !dirty.value || await confirmAction('离开当前群聊？', '尚未发送的消息和附件将被丢弃。', '离开') }
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
async function scrollBottom() { await nextTick(); if (timeline.value) timeline.value.scrollTop = timeline.value.scrollHeight; atBottom.value = true; unseen.value = 0; void markRead() }
function onScroll() { const el = timeline.value; if (!el) return; atBottom.value = el.scrollHeight - el.scrollTop - el.clientHeight < 80; if (atBottom.value) { unseen.value = 0; void markRead() } }
async function markRead() {
  const latest = messages.value.at(-1), id = selectedId.value, epoch = sessionEpoch.value, version = actionVersion
  if (!latest || document.visibilityState !== 'visible' || !atBottom.value || group.value?.membership !== 'join' || lastRead === latest.id) return
  lastRead = latest.id
  try { await groupApi.read(id, latest.id); if (isActionCurrent(version, epoch) && group.value?.id === id) upsertGroup({ ...group.value, unread_count: 0 }) }
  catch { if (isActionCurrent(version, epoch) && lastRead === latest.id) lastRead = '' }
}
async function older() { const el = timeline.value, height = el?.scrollHeight ?? 0; await loadHistory(); await nextTick(); if (el) el.scrollTop += el.scrollHeight - height }
async function loadMembers(more = false) {
  const id = selectedId.value, epoch = sessionEpoch.value, version = actionVersion
  if (!id || group.value?.membership !== 'join' || membersLoading.value || (more && !membersCursor.value)) return
  membersLoading.value = true; membersError.value = ''
  try { const page = await groupApi.members(id, more ? membersCursor.value! : undefined); if (!isActionCurrent(version, epoch)) return; members.value = more ? [...new Map([...members.value, ...page.members].map(m => [m.id, m])).values()] : page.members; membersCursor.value = page.next_cursor }
  catch (e) { if (isActionCurrent(version, epoch)) membersError.value = e instanceof Error ? e.message : '成员加载失败' }
  finally { if (isActionCurrent(version, epoch)) membersLoading.value = false }
}
async function create() {
  if (actionBusy.value || !consent.value || !createName.value.trim()) return
  const input = { name: createName.value.trim(), topic: createTopic.value, visibility: visibility.value, invite: createInvite.value.split(/[\s,，]+/).filter(Boolean), plaintext_consent: true as const }
  const signature = JSON.stringify(input)
  if (signature !== createSignature) { createTxn = crypto.randomUUID(); createSignature = signature }
  const epoch = sessionEpoch.value, version = actionVersion
  mutationBusy.value = true; actionError.value = ''
  try { const created = await groupApi.create({ ...input, transaction_id: createTxn }); if (!isActionCurrent(version, epoch)) return; upsertGroup(created); panel.value = null; createName.value = ''; createTopic.value = ''; createInvite.value = ''; consent.value = false; await router.push(groupPath(created.id)); if (active && epoch === sessionEpoch.value && selectedId.value === created.id && created.invite_failures?.length) actionError.value = `群组已创建，以下成员邀请失败，可在群详情中重试：${created.invite_failures.map(f => f.user_id + '（' + f.error + '）').join('、')}` }
  catch (e) { if (isActionCurrent(version, epoch)) actionError.value = e instanceof Error ? e.message : '创建失败，请重试' }
  finally { if (isActionCurrent(version, epoch)) mutationBusy.value = false }
}
async function join(item: Group) { if (!actionBusy.value) { actionError.value = ''; await acceptJoin(item) } }
async function leave() {
  const current = group.value
  if (!current || actionBusy.value) return
  const epoch = sessionEpoch.value, version = actionVersion
  const verb = current.membership === 'invite' ? '拒绝邀请' : current.membership === 'knock' ? '撤销申请' : '退出群组'
  if (!await confirmAction(`${verb}？`, '退出后不会因新消息自动加入。需要新的邀请或主动加入才能继续参与。', verb)) return
  if (!isActionCurrent(version, epoch) || actionBusy.value) return
  mutationBusy.value = true; actionError.value = ''
  status.value = `${verb}中…`
  try { await groupApi.leave(current.id); if (!isActionCurrent(version, epoch)) return; dirty.value = false; detailsOpen.value = false; upsertGroup({ ...current, membership: 'leave' }); await router.push('/groups'); if (active && epoch === sessionEpoch.value) void loadGroups() }
  catch (e) { if (isActionCurrent(version, epoch)) { status.value = ''; actionError.value = e instanceof Error ? e.message : '操作失败' } }
  finally { if (isActionCurrent(version, epoch)) mutationBusy.value = false }
}
async function remove(message: GroupMessage) {
  const id = selectedId.value, epoch = sessionEpoch.value, version = actionVersion
  if (actionBusy.value || !await confirmAction('删除这条消息？', '删除会同步到群内。无法收回已下载的附件、远端副本或截图。', '删除')) return
  if (!isActionCurrent(version, epoch) || actionBusy.value) return
  mutationBusy.value = true; actionError.value = ''; status.value = '正在删除消息…'
  try { await groupApi.remove(id, message.id); if (isActionCurrent(version, epoch)) { mergeMessages([{ ...message, redacted: true, body: '', attachments: [], reactions: [] }]); status.value = '消息已删除' } }
  catch (e) { if (isActionCurrent(version, epoch)) { status.value = ''; actionError.value = e instanceof Error ? e.message : '删除失败' } }
  finally { if (isActionCurrent(version, epoch)) mutationBusy.value = false }
}
async function react(message: GroupMessage, key: string, own: boolean) {
  if (!canSend.value || actionBusy.value) return
  const id = selectedId.value, epoch = sessionEpoch.value, version = actionVersion; mutationBusy.value = true; actionError.value = ''; status.value = '正在发送回应…'
  try { await groupApi.reaction(id, message.id, key, own); if (isActionCurrent(version, epoch)) { reactionDialog.value?.close(); reactionMessage.value = null; status.value = '回应已更新' } }
  catch (e) { if (isActionCurrent(version, epoch)) { status.value = ''; actionError.value = e instanceof Error ? e.message : '回应失败' } }
  finally { if (isActionCurrent(version, epoch)) mutationBusy.value = false }
}
function reactionPicker(message: GroupMessage) { reactionMessage.value = message; reactionDialog.value?.showModal() }
function customReaction(emoji: MastodonEmoji) { if (emoji.local_only) { actionError.value = '仅本站表情不能用于群聊回应。'; return } if (reactionMessage.value) void react(reactionMessage.value, `:${emoji.shortcode}:`, false) }
function receivedSent(incoming: GroupMessage[]) { mergeMessages(incoming); status.value = '消息已发送（本地服务已接受）'; void scrollBottom() }
async function context(message: GroupMessage, edit = false) { if (composerLocked.value) return; if (edit && dirty.value && !await confirmAction('替换当前草稿？', '编辑消息会用原消息内容替换当前输入框中的草稿。', '替换草稿')) return; if (edit) { editing.value = message; reply.value = null } else { reply.value = message; editing.value = null } }
onMounted(() => { document.addEventListener('visibilitychange', markRead) })
onBeforeUnmount(() => { active = false; actionVersion++; document.removeEventListener('visibilitychange', markRead) })
</script>
<template>
  <div class="groups-page" :class="{ 'groups-page--selected': !!selectedId, 'groups-page--panel': !!panel }">
    <aside class="groups-list" aria-label="我的群聊">
      <header class="groups-list__header"><h1>群组</h1></header>
      <div class="groups-list__tools"><button type="button" class="btn btn--primary btn--sm" @click="openPanel">创建群组</button></div>
      <div class="groups-list__search-field"><SearchBox embedded search-type="groups" allow-empty placeholder="群名或群地址" /></div>
      <p v-if="syncPaused && !selectedId" class="groups-notice" role="status">{{ syncError }} <button type="button" :disabled="refreshing" @click="refreshAfterPause">{{ refreshing ? '刷新中…' : '手动刷新并重试' }}</button></p>
      <p v-if="listError" class="groups-notice groups-notice--error" role="alert">{{ listError }} <button type="button" @click="loadGroups()">重试</button></p>
      <div class="groups-list__items">
<RouterLink v-for="item in groups" :key="item.id" :to="groupPath(item.id)" class="group-row" :class="{ 'group-row--active': item.id === selectedId }" :aria-current="item.id === selectedId ? 'page' : undefined"><GroupAvatar :name="item.name || '群组'" :url="item.avatar_url" /><span class="group-row__text"><strong>{{ item.name || '未命名群组' }}</strong><small>{{ item.membership !== 'join' ? groupMembershipLabel[item.membership] : item.muted ? '已静音' : item.encryption === 'encrypted' ? '加密群 · 使用客户端' : `${item.member_count ?? '未知'} 位成员` }}</small></span><span v-if="item.unread_count" class="group-row__badge" :aria-label="`${item.unread_count} 条未读`">{{ item.unread_count > 99 ? '99+' : item.unread_count }}</span></RouterLink>
        <p v-if="listLoading" class="groups-list__empty" role="status">正在加载群组…</p><p v-else-if="!groups.length && !listError" class="groups-list__empty">还没有群组。创建一个群，或通过搜索加入。</p><button v-if="listCursor" class="btn btn--ghost btn--sm" type="button" :disabled="listLoading" @click="loadGroups(true)">加载更多群组</button>
</div>
    </aside>
    <section class="groups-main" aria-label="群聊内容">
      <div v-if="panel" class="groups-setup">
<header><div><h2>创建群组</h2><p class="groups-setup__intro">为朋友或社区创建一个聊天空间。</p></div><button type="button" aria-label="关闭" :disabled="actionBusy" @click="panel = null">×</button></header>
        <p v-if="actionError" class="groups-notice groups-notice--error" role="alert">{{ actionError }}</p>
        <form class="group-setup-form" @submit.prevent="create"><label>群名称<input v-model="createName" required maxlength="255" placeholder="给群组起个名字" :disabled="actionBusy" /></label><label>群介绍<textarea v-model="createTopic" maxlength="512" rows="3" placeholder="聊些什么？（选填）" :disabled="actionBusy" /></label><fieldset><legend>谁可以加入</legend><label class="group-setup-choice"><input v-model="visibility" type="radio" value="private" :disabled="actionBusy" /><span>仅邀请<small>不出现在公开目录中，通过邀请加入。</small></span></label><label class="group-setup-choice"><input v-model="visibility" type="radio" value="public" :disabled="actionBusy" /><span>公开群<small>任何人可加入，并显示在本站公开目录中。</small></span></label></fieldset><GroupMemberInput v-model="createInvite" label="邀请成员（选填）" multiple :disabled="actionBusy" /><label class="group-setup-choice group-setup-consent"><input v-model="consent" type="checkbox" :disabled="actionBusy" /><span>我了解此群不会启用端到端加密<small>支持 Community 与 Matrix 成员。入群前的历史默认不可见。</small></span></label><button class="btn btn--primary" :disabled="actionBusy || !consent || !createName.trim()">{{ actionBusy ? '创建中…' : '创建群组' }}</button></form>
        <p v-if="status" class="groups-notice" role="status">{{ status }}</p><a v-if="externalClientURL" class="btn btn--secondary btn--sm" :href="externalClientURL" target="_blank" rel="noopener noreferrer">使用 Matrix 客户端打开</a>
      </div>
      <template v-else-if="selectedId">
        <header class="group-thread-header"><RouterLink to="/groups" class="group-thread-header__back" aria-label="返回群列表"><t-icon name="chevron-left" :size="24" /></RouterLink><GroupAvatar :name="group?.name || '群组'" :url="group?.avatar_url" :size="36" /><div><h2>{{ group?.name || (loading ? '正在加载…' : '群聊') }}</h2><p>{{ group ? `${groupMembershipLabel[group.membership]} · ${group.member_count ?? '未知'} 位成员` : '' }}</p></div><button v-if="group" class="btn btn--ghost btn--sm" type="button" :aria-expanded="detailsOpen" @click="detailsOpen = true">群详情</button></header>
        <p v-if="syncError" class="groups-notice" role="status">{{ syncError }} <template v-if="syncPaused"><button type="button" :disabled="refreshing" @click="refreshAfterPause">{{ refreshing ? '刷新中…' : '手动刷新并重试' }}</button><a v-if="clientURL" :href="clientURL" target="_blank" rel="noopener noreferrer">使用客户端打开</a></template></p><p v-if="error || actionError || joinError" class="groups-notice groups-notice--error" role="alert">{{ error || actionError || joinError }} <button v-if="error" type="button" @click="openRoom">重新加载</button></p>
        <div v-if="loading" class="groups-placeholder" role="status"><t-loading /><p>正在打开群聊…</p></div>
        <template v-else-if="group">
          <div v-if="group.membership !== 'join'" class="groups-state"><h3>{{ groupMembershipLabel[group.membership] }}</h3><p>{{ group.membership === 'invite' ? '有成员邀请你加入此群。接受后即可参与。' : group.membership === 'knock' ? '你的加入申请已提交，等待管理员批准。' : group.membership === 'ban' ? '你目前无法加入此群，请联系群管理员。' : '你已不在此群中，新消息不会自动将你加入。' }}</p><div><button v-if="['invite','leave','none'].includes(group.membership)" class="btn btn--primary btn--sm" type="button" :disabled="actionBusy" @click="join(group)">{{ joinBusy ? '加入中…' : joinOperation ? '重试查询' : group.membership === 'invite' ? '接受邀请' : '重新加入' }}</button><button v-if="['invite','knock'].includes(group.membership)" class="btn btn--secondary btn--sm" type="button" :disabled="actionBusy" @click="leave">{{ group.membership === 'invite' ? '拒绝邀请' : '撤销申请' }}</button></div></div>
          <div v-else-if="group.room_type === 'm.space'" class="groups-state"><h3>这是一个空间</h3><p>空间用于组织多个群组，请在 Matrix 客户端中浏览。</p><a v-if="clientURL" class="btn btn--primary btn--sm" :href="clientURL" target="_blank" rel="noopener noreferrer">使用客户端打开</a></div>
          <div v-else-if="group.encryption !== 'unencrypted'" class="groups-state"><t-icon name="lock-on" :size="36" /><h3>{{ group.encryption === 'encrypted' ? '请使用客户端参与加密群聊' : '正在确认加密状态' }}</h3><p>{{ group.encryption === 'encrypted' ? '你已加入此群。网页不支持端到端加密，无法读取或发送加密消息；未发送的草稿可以保留和复制。' : '确认之前，网页暂不发送消息或上传文件。' }}</p><a v-if="clientURL" class="btn btn--primary btn--sm" :href="clientURL" target="_blank" rel="noopener noreferrer">使用 Matrix 客户端打开</a><button v-if="group.encryption === 'unknown'" class="btn btn--secondary btn--sm" type="button" @click="openRoom">重新检查</button></div>
          <template v-else>
            <div v-if="group.replacement_id" class="groups-notice">此群已升级，旧群仅供查看。<RouterLink :to="{ name: 'search', query: { q: group.replacement_id, type: 'groups', via: group.via } }">查看新群</RouterLink></div>
            <div ref="timeline" class="group-timeline" role="log" aria-label="群消息" aria-live="off" @scroll="onScroll"><button v-if="historyCursor" class="group-history" type="button" :disabled="historyLoading" @click="older">{{ historyLoading ? '加载中…' : '查看更早的消息' }}</button><div v-if="!messages.length" class="groups-placeholder"><t-icon name="chat" :size="32" /><h3>开始聊天吧</h3><p>可以发送消息、图片、文件和表情。</p></div><GroupMessageView v-for="message in messages" :key="`${group.id}:${message.id}`" :message="message" :group-topic="group.topic" :reply="message.reply_to ? replyById.get(message.reply_to) : undefined" :can-send="canSend" :can-redact="group.capabilities.redact" :busy="actionBusy || composerLocked" @reply="context(message)" @edit="context(message, true)" @remove="remove(message)" @reaction="(key, own) => react(message, key, own)" @picker="reactionPicker(message)" /></div>
            <button v-if="unseen" class="group-new-messages" type="button" @click="scrollBottom">{{ unseen }} 条新消息 ↓</button>
          </template>
          <p v-if="typingNames && canSend" class="group-typing" role="status">{{ typingNames }} 正在输入…</p>
          <GroupComposer v-if="group.membership === 'join'" :key="group.id" :group="group" :reply="reply" :editing="editing" :members="members" @sent="receivedSent" @cancel-context="reply = null; editing = null" @dirty="dirty = $event" @locked="composerLocked = $event" />
          <p v-if="actionBusy" class="groups-notice" role="status">{{ joinBusy ? joinStatus : status }}</p><p class="groups-announcer" role="status">{{ status || joinStatus }}</p>
        </template>
      </template>
      <div v-else class="groups-welcome"><span class="groups-welcome__icon"><t-icon name="usergroup" :size="38" /></span><h2>一起聊聊</h2><p>创建群组，或加入其他服务器上的 Matrix 群聊。<br />图片、文件和表情都能在这里分享。</p><button class="btn btn--primary" type="button" :disabled="actionBusy" @click="openPanel">创建群组</button><p class="groups-subtle">在搜索框中输入群名或粘贴群地址，即可查找并加入。</p></div>
    </section>
    <dialog ref="detailsDialog" class="group-details-dialog" aria-label="群详情" @cancel="detailsOpen = false" @click="($event.target === detailsDialog) && (detailsOpen = false)"><GroupDetails v-if="group && detailsOpen" :group="group" :members="members" :members-cursor="membersCursor" :members-loading="membersLoading" :members-error="membersError" :pending="actionBusy" @close="detailsOpen = false" @updated="upsertGroup" @reload-members="loadMembers()" @more-members="loadMembers(true)" @leave="leave" /></dialog>
    <dialog ref="reactionDialog" class="group-reaction-dialog" aria-label="添加表情回应" @cancel="reactionMessage = null"><header><h2>添加表情回应</h2><button type="button" aria-label="关闭表情选择" @click="reactionDialog?.close(); reactionMessage = null">×</button></header><p v-if="actionError" class="groups-notice groups-notice--error" role="alert">{{ actionError }}</p><div class="group-reaction-unicode"><button v-for="emoji in ['👍','❤️','😂','🎉','🤔','😊','🙏','👀','😢','🚀','✅','✨']" :key="emoji" type="button" :disabled="actionBusy || !canSend" @click="reactionMessage && react(reactionMessage, emoji, false)">{{ emoji }}</button></div><EmojiPicker @select="customReaction" @close="reactionDialog?.close(); reactionMessage = null" /></dialog>
  </div>
</template>
<style scoped src="./groups.css"></style>
