<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { groupApi } from '../../infrastructure/groupApi'
import { safeGroupURL, fileSize, type Group, type GroupMember } from '../../domain/group'
import GroupAvatar from './GroupAvatar.vue'
const props = defineProps<{ group: Group; members: GroupMember[]; membersCursor: string | null; membersLoading: boolean; membersError?: string }>()
const emit = defineEmits<{ close: []; updated: [group: Group]; reloadMembers: []; moreMembers: []; leave: [] }>()
const name = ref(props.group.name), topic = ref(props.group.topic), invite = ref(''), error = ref(''), feedback = ref(''), busy = ref(false)
const confirmationBox = ref<HTMLElement>()
const memberConfirmation = ref<{ member: GroupMember; action: 'kick' | 'ban' } | null>(null)
const editingMember = ref(''), level = ref(0), fileInput = ref<HTMLInputElement>()
const clientURL = computed(() => safeGroupURL(props.group.client_url))
let alive = true
watch(() => props.group.id, () => { name.value = props.group.name; topic.value = props.group.topic; error.value = ''; feedback.value = '' })
async function act(action: () => Promise<unknown>, success: string) {
  if (busy.value) return
  busy.value = true; error.value = ''; feedback.value = ''
  try { await action(); if (alive) feedback.value = success }
  catch (e) { if (alive) error.value = e instanceof Error ? e.message : '操作失败，请重试' }
  finally { if (alive) busy.value = false }
}
async function save() {
  await act(async () => { const updated = await groupApi.update(props.group.id, { ...(props.group.capabilities.set_name ? { name: name.value.trim() } : {}), ...(props.group.capabilities.set_topic ? { topic: topic.value } : {}) }); if (alive) emit('updated', updated) }, '群资料已保存')
}
async function avatar(event: Event) {
  const file = (event.target as HTMLInputElement).files?.[0]
  if (!file) return
  if (file.size > (props.group.limits?.max_upload_bytes ?? 50 * 1024 * 1024)) { error.value = `图片不能超过 ${fileSize(props.group.limits?.max_upload_bytes ?? 50 * 1024 * 1024)}`; return }
  if (!/^image\/(png|jpeg|gif|webp)$/.test(file.type)) { error.value = '请选择 PNG、JPEG、GIF 或 WebP 图片'; return }
  await act(async () => { const upload = await groupApi.upload(props.group.id, file, new AbortController().signal, () => {}); const updated = await groupApi.update(props.group.id, { avatar_upload_id: upload.id }); if (alive) emit('updated', updated) }, '群头像已更新')
  ;(event.target as HTMLInputElement).value = ''
}
async function inviteMember() {
  await act(async () => { const updated = await groupApi.member(props.group.id, { user_id: invite.value.trim(), action: 'invite' }); if (alive) { emit('updated', updated); emit('reloadMembers'); invite.value = '' } }, '邀请已发送')
}
async function memberAction(member: GroupMember, action: 'kick' | 'ban' | 'unban' | 'approve') {
  if (action === 'kick' || action === 'ban') { memberConfirmation.value = { member, action }; await nextTick(); confirmationBox.value?.scrollIntoView({ block: 'nearest' }); confirmationBox.value?.querySelector('button')?.focus(); return }
  void performMemberAction(member, action)
}
async function performMemberAction(member: GroupMember, action: 'kick' | 'ban' | 'unban' | 'approve') {
  const labels = { kick: member.membership === 'knock' ? '拒绝申请' : '移出群组', ban: '封禁', unban: '解除封禁', approve: '批准加入' }
  await act(async () => { const updated = await groupApi.member(props.group.id, { user_id: member.id, action }); if (alive) { emit('updated', updated); emit('reloadMembers'); memberConfirmation.value = null } }, `${labels[action]}成功`)
}

async function permission() {
  await act(async () => { const updated = await groupApi.permissions(props.group.id, editingMember.value, level.value); if (alive) { emit('updated', updated); emit('reloadMembers'); editingMember.value = '' } }, '成员权限已更新')
}
async function copyAddress() { await act(() => navigator.clipboard.writeText(clientURL.value), '群地址已复制') }
async function mute() {
  const next = !props.group.muted
  await act(async () => { await groupApi.preferences(props.group.id, next); if (alive) emit('updated', { ...props.group, muted: next }) }, next ? '已静音此群' : '已恢复通知')
}
onBeforeUnmount(() => { alive = false })
</script>
<template>
  <section class="group-details" aria-label="群详情">
    <header><h2>群详情</h2><button type="button" aria-label="关闭群详情" @click="$emit('close')">×</button></header>
    <p v-if="error" class="group-details__error" role="alert">{{ error }}</p><p v-if="feedback" class="group-details__feedback" role="status">{{ feedback }}</p>
    <div v-if="memberConfirmation" ref="confirmationBox" class="group-details__confirm" role="alert"><strong>{{ memberConfirmation.action === 'ban' ? '封禁' : memberConfirmation.member.membership === 'knock' ? '拒绝申请' : '移出群组' }} {{ memberConfirmation.member.display_name || memberConfirmation.member.id }}？</strong><p>此操作会影响该成员继续参与群聊。</p><div><button class="btn btn--secondary btn--sm" type="button" :disabled="busy" @click="memberConfirmation = null">取消</button><button class="btn btn--primary btn--sm" type="button" :disabled="busy" @click="performMemberAction(memberConfirmation.member, memberConfirmation.action)">确认操作</button></div></div>
    <div class="group-details__identity"><GroupAvatar :name="group.name || '群组'" :url="group.avatar_url" :size="64" /><div><h3>{{ group.name || '未命名群组' }}</h3><p>{{ group.member_count ?? '未知' }} 位成员</p><button v-if="group.capabilities.set_avatar" class="btn btn--ghost btn--sm" type="button" :disabled="busy || group.encryption !== 'unencrypted'" @click="fileInput?.click()">更换头像</button><input ref="fileInput" hidden type="file" accept="image/png,image/jpeg,image/gif,image/webp" @change="avatar" /></div></div>
    <form v-if="group.capabilities.set_name || group.capabilities.set_topic" class="group-form" @submit.prevent="save"><label v-if="group.capabilities.set_name">群名称<input v-model="name" required maxlength="255" :disabled="busy" /></label><label v-if="group.capabilities.set_topic">群介绍<textarea v-model="topic" rows="3" maxlength="512" :disabled="busy" /></label><button class="btn btn--secondary btn--sm" :disabled="busy || (name === group.name && topic === group.topic)">保存资料</button></form>
    <p v-else-if="group.topic" class="group-details__topic">{{ group.topic }}</p>
    <div class="group-details__preferences"><span>群通知<small>与其他客户端同步</small></span><button type="button" role="switch" :aria-checked="!group.muted" :disabled="busy" @click="mute">{{ group.muted ? '已静音' : '已开启' }}</button></div>
    <p class="group-details__note">{{ group.encryption === 'encrypted' ? '此群已启用端到端加密，请使用客户端收发消息。' : group.encryption === 'unknown' ? '尚未确认此群的加密状态。' : '此群未启用端到端加密。' }}</p>
    <a v-if="clientURL" class="group-details__client" :href="clientURL" target="_blank" rel="noopener noreferrer">使用 Matrix 客户端打开 ↗</a><button v-if="clientURL" class="btn btn--ghost btn--sm" type="button" @click="copyAddress">复制群地址</button>
    <form v-if="group.capabilities.invite" class="group-form group-details__invite" @submit.prevent="inviteMember"><label>邀请成员<input v-model="invite" placeholder="本站用户名 或 @用户名:服务器" required maxlength="500" :disabled="busy" /></label><small>仅支持 Community 和 Matrix 账号。</small><button class="btn btn--secondary btn--sm" :disabled="busy || !invite.trim()">发送邀请</button></form>
    <div class="group-details__members-heading"><h3>成员</h3><button type="button" :disabled="membersLoading" @click="$emit('reloadMembers')">刷新</button></div>
    <p v-if="membersError" class="group-details__error" role="alert">{{ membersError }}</p>
    <ul class="group-details__members"><li v-for="member in members" :key="member.id"><div class="group-details__member"><GroupAvatar :name="member.display_name || member.id" :url="member.avatar_url" :size="32" /><span><strong>{{ member.display_name || member.id }}</strong><small>{{ member.id }}</small><small>{{ member.membership === 'join' ? '已加入' : member.membership === 'invite' ? '已邀请' : member.membership === 'knock' ? '申请加入' : member.membership === 'ban' ? '已封禁' : '已离开' }} · 权限 {{ member.power_level }}</small></span></div><details v-if="member.id !== group.current_user_id && (group.capabilities.kick || group.capabilities.ban || group.capabilities.manage_permissions || (member.membership === 'knock' && group.capabilities.invite))"><summary>管理成员</summary><div class="group-details__member-actions"><button v-if="member.membership === 'knock' && group.capabilities.invite" type="button" :disabled="busy" @click="memberAction(member, 'approve')">批准加入</button><button v-if="['join','invite','knock'].includes(member.membership) && group.capabilities.kick" type="button" :disabled="busy" @click="memberAction(member, 'kick')">{{ member.membership === 'knock' ? '拒绝申请' : '移出' }}</button><button v-if="group.capabilities.ban" type="button" :disabled="busy" @click="memberAction(member, member.membership === 'ban' ? 'unban' : 'ban')">{{ member.membership === 'ban' ? '解封' : '封禁' }}</button><button v-if="group.capabilities.manage_permissions" type="button" :disabled="busy" @click="editingMember = member.id; level = member.power_level">设置权限</button></div></details><form v-if="editingMember === member.id" class="group-form" @submit.prevent="permission"><label>成员权限等级<input v-model.number="level" type="number" step="1" required :disabled="busy" /></label><small>通常 0 为成员、50 为管理、100 为管理员；实际权限由群规则决定，不能设置超出自身权限的等级。</small><div><button class="btn btn--secondary btn--sm" :disabled="busy">保存</button><button class="btn btn--ghost btn--sm" type="button" @click="editingMember = ''">取消</button></div></form></li></ul>
    <p v-if="membersLoading" role="status">正在加载成员…</p><button v-if="membersCursor" class="btn btn--ghost btn--sm" type="button" :disabled="membersLoading" @click="$emit('moreMembers')">更多成员</button>
    <button class="group-details__leave" type="button" :disabled="busy" @click="$emit('leave')">{{ group.membership === 'invite' ? '拒绝邀请' : group.membership === 'knock' ? '撤销申请' : '退出群组' }}</button>
  </section>
</template>
<style scoped>
.group-details { padding:1.25rem; border-left:1px solid var(--color-separator); background:var(--color-bg); min-width:0; overflow:auto; }.group-details>header { display:flex; align-items:center; justify-content:space-between; margin-bottom:1.25rem; }.group-details h2 { font-size:1.05rem; }.group-details h3 { font-size:.9rem; overflow-wrap:anywhere; }.group-details>header button { width:36px; height:36px; border-radius:10px; font-size:1.5rem; background:var(--color-fill); }.group-details__confirm { padding:1rem; margin-bottom:1rem; border:1px solid var(--color-danger); border-radius:12px; font-size:.85rem; overflow-wrap:anywhere; }.group-details__confirm p { margin:.5rem 0; color:var(--color-label-secondary); }.group-details__confirm>div { display:flex; gap:.5rem; }.group-details__identity { display:flex; align-items:center; gap:.8rem; margin-bottom:1.25rem; }.group-details__identity>div {min-width:0}.group-details__identity p,.group-details small,.group-details__note { color:var(--color-label-secondary); font-size:.75rem; }.group-details__topic { white-space:pre-wrap; overflow-wrap:anywhere; font-size:.85rem; }.group-details__preferences {display:flex;justify-content:space-between;align-items:center;padding:1rem 0;margin-top:1rem;border-top:1px solid var(--color-separator);font-size:.9rem}.group-details__preferences small{display:block}.group-details__preferences button {background:var(--color-accent-soft);color:var(--color-accent-text);padding:.45rem .75rem;border-radius:9px}.group-details__client {display:block;margin:.75rem 0;color:var(--color-accent-text);font-size:.8rem}.group-details__invite {border-top:1px solid var(--color-separator);padding-top:1.25rem;margin-top:1.25rem}.group-details__members-heading {display:flex;justify-content:space-between;align-items:center;margin-top:1.5rem}.group-details__members-heading button{color:var(--color-accent-text);font-size:.8rem;min-height:36px}.group-details__members {list-style:none;padding:0;margin:0}.group-details__members>li {padding:.8rem 0;border-bottom:1px solid var(--color-separator)}.group-details__member {display:flex;gap:.6rem;align-items:center}.group-details__member>span:not(.group-avatar) {min-width:0;flex:1}.group-details__member strong,.group-details__member small {display:block;overflow-wrap:anywhere}.group-details__member strong {font-size:.85rem}.group-details summary {margin-top:.4rem;color:var(--color-label-secondary);font-size:.75rem;cursor:pointer}.group-details__member-actions {display:flex;flex-wrap:wrap;gap:.5rem;margin-top:.35rem}.group-details__member-actions button {padding:.35rem .5rem;background:var(--color-fill);border-radius:6px;font-size:.75rem}.group-details__leave {display:block;width:100%;padding:.8rem;margin-top:1.5rem;border:1px solid var(--color-separator);border-radius:10px;color:var(--color-danger);font-weight:600}.group-details__error {color:var(--color-danger);font-size:.85rem;margin-bottom:1rem;overflow-wrap:anywhere}.group-details__feedback {color:var(--color-accent-text);font-size:.85rem;margin-bottom:1rem}.group-form {display:grid;gap:.75rem}.group-form label {display:grid;gap:.4rem;font-size:.8rem;font-weight:550}.group-form input,.group-form textarea {width:100%;min-width:0;min-height:40px;border:1px solid var(--color-separator);border-radius:9px;padding:.6rem .7rem;background:var(--color-bg-row);color:var(--color-label);font:inherit}.group-form textarea {resize:vertical}.group-form button{justify-self:start}.group-details button:disabled{opacity:.45}.group-details :is(button,input,textarea,summary):focus-visible{outline:2px solid var(--color-accent);outline-offset:2px}@media(pointer:coarse){.group-details button,.group-details input,.group-details summary{min-height:44px}.group-details summary{display:flex;align-items:center}}
</style>
