<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { groupApi } from '../../infrastructure/groupApi'
import { fileSize, type Group, type GroupMember } from '../../domain/group'
import GroupAvatar from './GroupAvatar.vue'
import GroupMemberInput from './GroupMemberInput.vue'
import GroupAddressEditor from './GroupAddressEditor.vue'
const props = defineProps<{ group: Group; members: GroupMember[]; membersCursor: string | null; membersLoading: boolean; membersError?: string; pending?: boolean }>()
const emit = defineEmits<{ close: []; updated: [group: Group]; reloadMembers: []; moreMembers: []; leave: [] }>()
const name = ref(props.group.name), topic = ref(props.group.topic), invite = ref(''), error = ref(''), feedback = ref(''), working = ref(false), progress = ref('')
const busy = computed(() => working.value || props.pending)
const confirmationBox = ref<HTMLElement>()
const memberConfirmation = ref<{ member: GroupMember; action: 'kick' | 'ban' } | null>(null)
const editingMember = ref(''), level = ref(0), fileInput = ref<HTMLInputElement>()
let alive = true, generation = 0
watch([() => props.group.id, sessionEpoch], () => { generation++; working.value = false; name.value = props.group.name; topic.value = props.group.topic; error.value = ''; feedback.value = '' }, { flush: 'sync' })
async function act(action: (valid: () => boolean) => Promise<unknown>, success: string, pending = '正在处理…') {
  if (busy.value) return
  const version = generation, epoch = sessionEpoch.value
  const valid = () => alive && version === generation && epoch === sessionEpoch.value
  working.value = true; progress.value = pending; error.value = ''; feedback.value = ''
  try { await action(valid); if (valid()) feedback.value = success }
  catch (e) { if (valid()) error.value = e instanceof Error ? e.message : '操作失败，请重试' }
  finally { if (valid()) working.value = false }
}
async function save() {
  await act(async valid => { const updated = await groupApi.update(props.group.id, { ...(props.group.capabilities.set_name ? { name: name.value.trim() } : {}), ...(props.group.capabilities.set_topic ? { topic: topic.value } : {}) }); if (valid()) emit('updated', updated) }, '群资料已保存', '正在保存资料…')
}
async function avatar(event: Event) {
  const file = (event.target as HTMLInputElement).files?.[0]
  if (!file) return
  if (file.size > (props.group.limits?.max_upload_bytes ?? 50 * 1024 * 1024)) { error.value = `图片不能超过 ${fileSize(props.group.limits?.max_upload_bytes ?? 50 * 1024 * 1024)}`; return }
  if (!/^image\/(png|jpeg|gif|webp)$/.test(file.type)) { error.value = '请选择 PNG、JPEG、GIF 或 WebP 图片'; return }
  await act(async valid => { const upload = await groupApi.upload(props.group.id, file, new AbortController().signal, () => {}); if (!valid()) return; const updated = await groupApi.update(props.group.id, { avatar_upload_id: upload.id }); if (valid()) emit('updated', updated) }, '群头像已更新', '正在上传头像…')
  ;(event.target as HTMLInputElement).value = ''
}
async function inviteMember() {
  await act(async valid => { const updated = await groupApi.member(props.group.id, { user_id: invite.value.trim(), action: 'invite' }); if (valid()) { emit('updated', updated); emit('reloadMembers'); invite.value = '' } }, '邀请已发送', '正在发送邀请…')
}
async function memberAction(member: GroupMember, action: 'kick' | 'ban' | 'unban' | 'approve') {
  if (action === 'kick' || action === 'ban') { memberConfirmation.value = { member, action }; await nextTick(); confirmationBox.value?.scrollIntoView({ block: 'nearest' }); confirmationBox.value?.querySelector('button')?.focus(); return }
  void performMemberAction(member, action)
}
async function performMemberAction(member: GroupMember, action: 'kick' | 'ban' | 'unban' | 'approve') {
  const labels = { kick: member.membership === 'knock' ? '拒绝申请' : '移出群组', ban: '封禁', unban: '解除封禁', approve: '批准加入' }
  await act(async valid => { const updated = await groupApi.member(props.group.id, { user_id: member.id, action }); if (valid()) { emit('updated', updated); emit('reloadMembers'); memberConfirmation.value = null } }, `${labels[action]}成功`)
}

async function permission() {
  await act(async valid => { const updated = await groupApi.permissions(props.group.id, editingMember.value, level.value); if (valid()) { emit('updated', updated); emit('reloadMembers'); editingMember.value = '' } }, '成员权限已更新')
}
async function mute() {
  const next = !props.group.muted
  await act(async valid => { await groupApi.preferences(props.group.id, next); if (valid()) emit('updated', { ...props.group, muted: next }) }, next ? '已静音此群' : '已恢复通知')
}
onBeforeUnmount(() => { alive = false })
</script>
<template>
  <section class="group-details" aria-label="群详情" :aria-busy="busy">
    <header class="group-details__header"><h2>群详情</h2><button type="button" aria-label="关闭群详情" @click="$emit('close')">×</button></header>
    <div class="group-details__body">
    <p v-if="working" class="group-details__pending" role="status">{{ progress }}</p>
    <p v-if="error" class="group-details__error" role="alert">{{ error }}</p><p v-if="feedback" class="group-details__feedback" role="status">{{ feedback }}</p>
    <div v-if="memberConfirmation" ref="confirmationBox" class="group-details__confirm" role="alert"><strong>{{ memberConfirmation.action === 'ban' ? '封禁' : memberConfirmation.member.membership === 'knock' ? '拒绝申请' : '移出群组' }} {{ memberConfirmation.member.display_name || memberConfirmation.member.id }}？</strong><p>此操作会影响该成员继续参与群聊。</p><div><button class="btn btn--secondary btn--sm" type="button" :disabled="busy" @click="memberConfirmation = null">取消</button><button class="btn btn--primary btn--sm" type="button" :disabled="busy" @click="performMemberAction(memberConfirmation.member, memberConfirmation.action)">确认操作</button></div></div>
    <div class="group-details__identity"><GroupAvatar :name="group.name || '群组'" :url="group.avatar_url" :size="64" /><div><h3>{{ group.name || '未命名群组' }}</h3><p>{{ group.member_count ?? '未知' }} 位成员</p><button v-if="group.capabilities.set_avatar" class="btn btn--ghost btn--sm" type="button" :disabled="busy || group.encryption !== 'unencrypted'" @click="fileInput?.click()">更换头像</button><input ref="fileInput" hidden type="file" accept="image/png,image/jpeg,image/gif,image/webp" @change="avatar" /></div></div>
    <form v-if="group.capabilities.set_name || group.capabilities.set_topic" class="group-form group-details__profile" @submit.prevent="save"><label v-if="group.capabilities.set_name">群名称<input v-model="name" required maxlength="255" :disabled="busy" /></label><label v-if="group.capabilities.set_topic">群介绍<textarea v-model="topic" rows="3" maxlength="512" :disabled="busy" /></label><button class="btn btn--secondary btn--sm" :disabled="busy || (name === group.name && topic === group.topic)">{{ working && progress === '正在保存资料…' ? '保存中…' : '保存资料' }}</button></form>
    <p v-else-if="group.topic" class="group-details__topic">{{ group.topic }}</p>
    <div class="group-details__preferences"><span>群通知<small>与其他客户端同步</small></span><button type="button" role="switch" :aria-checked="!group.muted" :disabled="busy" @click="mute">{{ group.muted ? '已静音' : '已开启' }}</button></div>
    <p class="group-details__note">{{ group.encryption === 'encrypted' ? '此群已启用端到端加密，请使用客户端收发消息。' : group.encryption === 'unknown' ? '尚未确认此群的加密状态。' : '此群未启用端到端加密。' }}</p>
    <GroupAddressEditor :group="group" @updated="$emit('updated', $event)" />
    <form v-if="group.capabilities.invite" class="group-form group-details__invite" @submit.prevent="inviteMember"><GroupMemberInput v-model="invite" required :disabled="busy" :exclude="[group.current_user_id || '', ...members.filter(m => ['join', 'invite', 'ban'].includes(m.membership)).map(m => m.id)]" /><button class="btn btn--secondary btn--sm" :disabled="busy || !invite.trim()">{{ working && progress === '正在发送邀请…' ? '邀请中…' : '发送邀请' }}</button></form>
    <div class="group-details__members-heading"><h3>成员<span v-if="group.member_count != null" class="group-details__member-count">{{ group.member_count }}</span></h3><button type="button" :disabled="membersLoading" @click="$emit('reloadMembers')">{{ membersLoading ? '加载中…' : '刷新' }}</button></div>
    <p v-if="membersError" class="group-details__error" role="alert">{{ membersError }}</p>
    <ul class="group-details__members"><li v-for="member in members" :key="member.id"><div class="group-details__member"><GroupAvatar :name="member.display_name || member.id" :url="member.avatar_url" :size="32" /><span><strong>{{ member.display_name || member.id }}</strong><small>{{ member.id }}</small><small>{{ member.membership === 'join' ? '已加入' : member.membership === 'invite' ? '已邀请' : member.membership === 'knock' ? '申请加入' : member.membership === 'ban' ? '已封禁' : '已离开' }} · 权限 {{ member.power_level }}</small></span></div><details v-if="member.id !== group.current_user_id && (group.capabilities.kick || group.capabilities.ban || group.capabilities.manage_permissions || (member.membership === 'knock' && group.capabilities.invite))"><summary>管理成员</summary><div class="group-details__member-actions"><button v-if="member.membership === 'knock' && group.capabilities.invite" type="button" :disabled="busy" @click="memberAction(member, 'approve')">批准加入</button><button v-if="['join','invite','knock'].includes(member.membership) && group.capabilities.kick" type="button" :disabled="busy" @click="memberAction(member, 'kick')">{{ member.membership === 'knock' ? '拒绝申请' : '移出' }}</button><button v-if="group.capabilities.ban" type="button" :disabled="busy" @click="memberAction(member, member.membership === 'ban' ? 'unban' : 'ban')">{{ member.membership === 'ban' ? '解封' : '封禁' }}</button><button v-if="group.capabilities.manage_permissions" type="button" :disabled="busy" @click="editingMember = member.id; level = member.power_level">设置权限</button></div></details><form v-if="editingMember === member.id" class="group-form" @submit.prevent="permission"><label>成员权限等级<input v-model.number="level" type="number" step="1" required :disabled="busy" /></label><small>通常 0 为成员、50 为管理、100 为管理员；实际权限由群规则决定，不能设置超出自身权限的等级。</small><div><button class="btn btn--secondary btn--sm" :disabled="busy">保存</button><button class="btn btn--ghost btn--sm" type="button" @click="editingMember = ''">取消</button></div></form></li></ul>
    <p v-if="membersLoading" role="status">正在加载成员…</p><button v-if="membersCursor" class="btn btn--ghost btn--sm" type="button" :disabled="membersLoading" @click="$emit('moreMembers')">更多成员</button>
    <button class="group-details__leave" type="button" :disabled="busy" @click="$emit('leave')">{{ group.membership === 'invite' ? '拒绝邀请' : group.membership === 'knock' ? '撤销申请' : '退出群组' }}</button>
    </div>
  </section>
</template>
<style scoped>
.group-details__pending { margin-bottom:var(--space-4); color:var(--color-label-secondary); font-size:var(--text-subhead); }
.group-details { min-width:0; background:var(--color-bg); }
.group-details__header { position:sticky; top:0; z-index:3; display:flex; align-items:center; justify-content:space-between; gap:var(--space-3); padding:var(--space-4) var(--space-5); border-bottom:1px solid var(--color-separator); background:var(--color-bg); }
.group-details h2 { font-size:var(--text-headline); line-height:var(--leading-headline); }
.group-details h3 { font-size:var(--text-subhead); line-height:var(--leading-subhead); overflow-wrap:anywhere; }
.group-details__header button { flex:none; width:40px; height:40px; border-radius:var(--radius-md); font-size:1.5rem; background:var(--color-fill); }
.group-details__body { padding:var(--space-5); padding-bottom:max(var(--space-5),var(--safe-bottom)); }
.group-details__confirm { padding:var(--space-4); margin-bottom:var(--space-4); border:1px solid var(--color-danger); border-radius:var(--radius-md); font-size:var(--text-subhead); overflow-wrap:anywhere; background:var(--color-danger-soft); }
.group-details__confirm p { margin:var(--space-2) 0; color:var(--color-label-secondary); }
.group-details__confirm>div { display:flex; flex-wrap:wrap; gap:var(--space-2); }
.group-details__identity { display:flex; align-items:center; gap:var(--space-4); margin-bottom:var(--space-6); }
.group-details__identity>div { min-width:0; }
.group-details__identity h3 { font-size:var(--text-headline); }
.group-details__identity p { margin-top:var(--space-1); }
.group-details__identity .btn { margin-top:var(--space-1); margin-left:calc(-1 * var(--space-3)); }
.group-details small,.group-details__identity p,.group-details__note { color:var(--color-label-secondary); font-size:var(--text-caption); line-height:var(--leading-body); }
.group-details__topic { white-space:pre-wrap; overflow-wrap:anywhere; font-size:var(--text-subhead); line-height:var(--leading-cjk); }
.group-details__preferences { display:flex; justify-content:space-between; align-items:center; gap:var(--space-3); padding:var(--space-4); margin-top:var(--space-6); border:1px solid var(--color-separator); border-radius:var(--radius-md); background:var(--color-bg-grouped); font-size:var(--text-subhead); font-weight:var(--weight-medium); }
.group-details__preferences small { display:block; margin-top:var(--space-1); font-weight:var(--weight-regular); }
.group-details__preferences button { flex:none; min-height:40px; background:var(--color-accent-soft); color:var(--color-accent-text); padding:var(--space-2) var(--space-3); border-radius:var(--radius-sm); font-weight:var(--weight-semibold); }
.group-details__preferences button[aria-checked=false] { background:var(--color-fill); color:var(--color-label-secondary); }
.group-details__note { margin-top:var(--space-3); }
.group-details__invite { border-top:1px solid var(--color-separator); padding-top:var(--space-5); margin-top:var(--space-5); }
.group-details__members-heading { display:flex; justify-content:space-between; align-items:center; gap:var(--space-3); margin-top:var(--space-6); padding-bottom:var(--space-2); }
.group-details__members-heading h3 { display:flex; align-items:center; gap:var(--space-2); }
.group-details__member-count { font-size:var(--text-caption); font-weight:var(--weight-medium); color:var(--color-label-secondary); background:var(--color-fill); min-width:22px; padding:2px 6px; text-align:center; border-radius:var(--radius-full); }
.group-details__members-heading button { color:var(--color-accent-text); font-size:var(--text-footnote); min-height:40px; padding:0 var(--space-2); border-radius:var(--radius-sm); }
.group-details__members { list-style:none; padding:0; margin:0; }
.group-details__members>li { padding:var(--space-4) 0; border-bottom:1px solid var(--color-separator); }
.group-details__member { display:flex; gap:var(--space-3); align-items:center; }
.group-details__member>span:not(.group-avatar) { min-width:0; flex:1; }
.group-details__member strong,.group-details__member small { display:block; overflow-wrap:anywhere; }
.group-details__member strong { font-size:var(--text-subhead); line-height:var(--leading-body); }
.group-details__member small { margin-top:2px; }
.group-details__members details { margin-left:44px; }
.group-details summary { min-height:36px; margin-top:var(--space-1); padding-block:var(--space-2); color:var(--color-label-secondary); font-size:var(--text-caption); cursor:pointer; }
.group-details__member-actions { display:flex; flex-wrap:wrap; gap:var(--space-2); margin-top:var(--space-1); }
.group-details__member-actions button { min-height:36px; padding:var(--space-2); background:var(--color-fill); border-radius:var(--radius-sm); font-size:var(--text-footnote); }
.group-details__members .group-form { margin-top:var(--space-3); padding:var(--space-3); border-radius:var(--radius-md); background:var(--color-bg-grouped); }
.group-details__leave { display:block; width:100%; min-height:44px; padding:var(--space-3); margin-top:var(--space-6); border:1px solid var(--color-separator); border-radius:var(--radius-md); color:var(--color-danger); font-size:var(--text-subhead); font-weight:var(--weight-semibold); }
.group-details__error,.group-details__feedback { padding:var(--space-3); border-radius:var(--radius-md); font-size:var(--text-subhead); margin-bottom:var(--space-4); overflow-wrap:anywhere; line-height:var(--leading-body); }
.group-details__error { color:var(--color-danger); background:var(--color-danger-soft); }
.group-details__feedback { color:var(--color-success); background:var(--color-success-soft); }
.group-form { display:grid; gap:var(--space-4); }
.group-form label { display:grid; gap:var(--space-2); font-size:var(--text-subhead); font-weight:var(--weight-medium); }
.group-form input,.group-form textarea { width:100%; min-width:0; min-height:44px; border:1px solid var(--color-separator); border-radius:var(--radius-md); padding:var(--space-3); background:var(--color-bg-row); color:var(--color-label); font:inherit; font-size:1rem; font-weight:var(--weight-regular); }
.group-form textarea { resize:vertical; line-height:var(--leading-body); }
.group-form button { justify-self:start; }
.group-details .btn { min-height:40px; border-radius:var(--radius-md); }
.group-details button:disabled { opacity:.45; cursor:default; }
.group-details button:active:not(:disabled) { background:var(--color-pressed); }
.group-details :is(button,input,textarea,summary):focus-visible { outline:2px solid var(--color-accent); outline-offset:2px; }
@media(max-width:360px) { .group-details__header,.group-details__body { padding-inline:var(--space-4); } }
@media(pointer:coarse) { .group-details button,.group-details input,.group-details summary { min-height:var(--hit-target); } }
</style>
