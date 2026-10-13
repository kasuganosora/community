import { isAxiosError } from 'axios'
import { computed, onBeforeUnmount, ref, watch, type Ref } from 'vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { groupApi } from '../../infrastructure/groupApi'
import { groupCanSend, mergeGroupMessages, type Group, type GroupMessage } from '../../domain/group'
export function useGroups(selectedId: Ref<string>) {
  const groups = ref<Group[]>([])
  const group = ref<Group | null>(null)
  const messages = ref<GroupMessage[]>([])
  const listCursor = ref<string | null>(null)
  const historyCursor = ref<string | null>(null)
  const loading = ref(false), listLoading = ref(false), historyLoading = ref(false)
  const error = ref(''), listError = ref(''), syncError = ref('')
  const syncPaused = ref(false), refreshing = ref(false)
  const canSend = computed(() => groupCanSend(group.value))
  let version = 0, active = true, syncController: AbortController | undefined, roomController: AbortController | undefined
  let retryTimer: ReturnType<typeof setTimeout> | undefined
  let resumeWait: (() => void) | undefined
  let syncCursor: string | undefined
  let initialSyncDone = false
  const isCurrent = (id: string, epoch: number) => active && epoch === sessionEpoch.value && id === selectedId.value
  function upsertGroup(item: Group) {
    const index = groups.value.findIndex(g => g.id === item.id)
    if (['join', 'invite', 'knock'].includes(item.membership)) {
      if (index < 0) groups.value.push(item); else groups.value[index] = item
    } else if (index >= 0) groups.value.splice(index, 1)
    if (selectedId.value === item.id) group.value = item
  }
  async function loadGroups(more = false) {
    if (listLoading.value || (more && !listCursor.value)) return
    const epoch = sessionEpoch.value; listLoading.value = true
    try {
      const page = await groupApi.list(more ? listCursor.value! : undefined)
      if (!active || epoch !== sessionEpoch.value) return
      groups.value = more ? [...new Map([...groups.value, ...page.groups].map(g => [g.id, g])).values()] : page.groups
      listCursor.value = page.next_cursor; listError.value = ''
    } catch (e) { if (active && epoch === sessionEpoch.value) listError.value = e instanceof Error ? e.message : '群组加载失败' }
    finally { if (active && epoch === sessionEpoch.value) listLoading.value = false }
  }
  function revokeInaccessible(error: unknown) {
    if (isAxiosError(error) && [403, 404].includes(error.response?.status ?? 0)) {
      messages.value = []; historyCursor.value = null
      if (group.value) upsertGroup({ ...group.value, membership: 'leave', capabilities: { ...group.value.capabilities, send_message: false } })
    }
  }
  async function refreshRoom() {
    const id = selectedId.value, epoch = sessionEpoch.value
    if (!id) return
    try { const value = await groupApi.get(id); if (isCurrent(id, epoch)) { upsertGroup(value); error.value = '' } }
    catch (e) { if (isCurrent(id, epoch)) { error.value = e instanceof Error ? e.message : '群信息更新失败'; revokeInaccessible(e) } }
  }
  async function openRoom() {
    const request = ++version, id = selectedId.value, epoch = sessionEpoch.value
    roomController?.abort(); roomController = new AbortController()
    const existing = group.value?.id === id
    const previousMessages = existing ? messages.value : []
    if (!existing) { group.value = null; messages.value = []; historyCursor.value = null }
    error.value = ''; historyLoading.value = false
    if (!id) { loading.value = false; return }
    loading.value = !existing
    try {
      const value = await groupApi.get(id, roomController.signal)
      if (!isCurrent(id, epoch) || request !== version) return
      upsertGroup(value)
      if (value.membership === 'join' && value.encryption === 'unencrypted' && value.room_type !== 'm.space') {
        const page = await groupApi.messages(id, undefined, roomController.signal)
        if (!isCurrent(id, epoch) || request !== version) return
        const previousById = new Map(previousMessages.map(message => [message.id, message]))
        const arrivedDuringRequest = messages.value.filter(message => previousById.get(message.id) !== message)
        messages.value = mergeGroupMessages(mergeGroupMessages(previousMessages, page.messages), arrivedDuringRequest)
        historyCursor.value = page.next_cursor
      }
    } catch (e) { if (isCurrent(id, epoch) && request === version) { error.value = e instanceof Error ? e.message : '群聊加载失败'; revokeInaccessible(e) } }
    finally { if (isCurrent(id, epoch) && request === version) loading.value = false }
  }
  async function loadHistory() {
    const id = selectedId.value, epoch = sessionEpoch.value
    if (!historyCursor.value || historyLoading.value || !id) return
    historyLoading.value = true
    try {
      const page = await groupApi.messages(id, historyCursor.value)
      if (!isCurrent(id, epoch)) return
      messages.value = mergeGroupMessages(messages.value, page.messages, true)
      historyCursor.value = page.next_cursor; error.value = ''
    } catch (e) { if (isCurrent(id, epoch)) error.value = e instanceof Error ? e.message : '历史加载失败' }
    finally { if (isCurrent(id, epoch)) historyLoading.value = false }
  }
  async function syncLoop() {
    const epoch = sessionEpoch.value
    while (active && epoch === sessionEpoch.value) {
      syncController = new AbortController()
      try {
        const update = await groupApi.sync(syncCursor, syncController.signal)
        if (!active || epoch !== sessionEpoch.value) return
        const initial = !initialSyncDone
        syncCursor = update.next_cursor; syncError.value = ''; initialSyncDone = true
        if (update.reset) { await loadGroups() }
        for (const item of update.groups ?? []) upsertGroup(item)
        for (const left of update.left_group_ids ?? []) {
          groups.value = groups.value.filter(item => item.id !== left)
          if (group.value?.id === left) group.value = { ...group.value, membership: 'leave' }
        }
        if (update.reset) { messages.value = []; historyCursor.value = null }
        if (initial || update.reset) await openRoom()
        const incoming = update.messages_by_group?.[selectedId.value]
        if (incoming) messages.value = mergeGroupMessages(messages.value, incoming)
        if (group.value && update.typing_by_group?.[group.value.id]) group.value.typing_users = update.typing_by_group[group.value.id]
        if (update.changed_group_ids?.includes(selectedId.value) && !update.groups?.some(item => item.id === selectedId.value)) await refreshRoom()
        // Some servers return immediately when the snapshot changes; yield before the next long poll.
        if (!update.has_more) await new Promise<void>(resolve => { resumeWait = resolve; retryTimer = setTimeout(resolve, 150) })
      } catch (error) {
        if (!active || epoch !== sessionEpoch.value) return
        if (isAxiosError(error) && error.response?.data?.code === 'sync_limit_exceeded') {
          syncPaused.value = true
          syncError.value = typeof error.response.data.error === 'string' ? error.response.data.error : '群组数量超过网页实时同步上限，请使用 Matrix 客户端。'
          syncError.value += ' 实时同步已暂停；仍可切换群组、查看历史或手动刷新。'
          const needsInitialHistory = !initialSyncDone
          // A permanent sync limit must not lock the room/history UI behind a waterline that cannot arrive.
          initialSyncDone = true
          if (needsInitialHistory) await openRoom()
          return
        }
        if (isAxiosError(error) && error.response?.data?.code === 'invalid_cursor') { syncCursor = undefined; initialSyncDone = false }
        syncError.value = '连接暂时中断，正在自动重新连接。草稿会保留。'
        await new Promise<void>(resolve => { resumeWait = resolve; retryTimer = setTimeout(resolve, 3000) })
      }
    }
  }
  async function refreshAfterPause() {
    if (!active || refreshing.value || !syncPaused.value) return
    const epoch = sessionEpoch.value
    refreshing.value = true
    try {
      await Promise.all([loadGroups(), openRoom()])
      if (!active || epoch !== sessionEpoch.value) return
      // Only an explicit user action restarts the stopped loop. Rebuild the cursor,
      // since an oversized cursor is itself one of the permanent-limit causes.
      syncCursor = undefined
      syncPaused.value = false
      syncError.value = '正在重新检查实时同步…'
      void syncLoop()
    } finally { if (active && epoch === sessionEpoch.value) refreshing.value = false }
  }
  function mergeMessages(incoming: GroupMessage[]) { messages.value = mergeGroupMessages(messages.value, incoming) }
  watch(selectedId, () => { if (initialSyncDone) void openRoom(); else loading.value = !!selectedId.value }, { immediate: true })
  watch(sessionEpoch, () => {
    version++; syncController?.abort(); roomController?.abort(); clearTimeout(retryTimer); resumeWait?.()
    groups.value = []; group.value = null; messages.value = []; syncCursor = undefined; initialSyncDone = false
    listLoading.value = false; loading.value = false; listCursor.value = null; historyCursor.value = null
    listError.value = ''; error.value = ''; syncError.value = ''; syncPaused.value = false; refreshing.value = false
    void loadGroups(); void syncLoop()
  })
  void loadGroups(); void syncLoop()
  onBeforeUnmount(() => { active = false; version++; syncController?.abort(); roomController?.abort(); clearTimeout(retryTimer); resumeWait?.() })
  return { groups, group, messages, listCursor, historyCursor, loading, listLoading, historyLoading, error, listError, syncError, syncPaused, refreshing, refreshAfterPause, canSend, loadGroups, refreshRoom, openRoom, loadHistory, upsertGroup, mergeMessages }
}
