import { isAxiosError } from 'axios'
import { computed, onBeforeUnmount, ref, watch, type Ref } from 'vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { groupApi } from '../../infrastructure/groupApi'
import { groupCanSend, mergeGroupMessages, type Group, type GroupMessage } from '../../domain/group'
import { t } from '@/shared/i18n'
import { translateApiError } from '@/shared/i18n/apiErrors'
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
  let version = 0, active = true, syncController: AbortController | undefined, roomController: AbortController | undefined, listController: AbortController | undefined
  let revision = 0
  const groupChanges = new Map<string, number>()
  let retryTimer: ReturnType<typeof setTimeout> | undefined
  let resumeWait: (() => void) | undefined
  // Live sync pauses while the page is hidden (battery): the in-flight long
  // poll finishes, then the loop waits for visibilitychange before polling again.
  let hiddenWait: (() => void) | undefined
  const releaseHidden = () => { const resolve = hiddenWait; hiddenWait = undefined; resolve?.() }
  const onVisibility = () => { if (!document.hidden) releaseHidden() }
  if (typeof document !== 'undefined') document.addEventListener('visibilitychange', onVisibility)
  let syncCursor: string | undefined
  let initialSyncDone = false
  let initialFallbackStarted = false
  let initialHistoryTimer: ReturnType<typeof setTimeout> | undefined
  function scheduleInitialHistory() {
    clearTimeout(initialHistoryTimer)
    const epoch = sessionEpoch.value
    initialHistoryTimer = setTimeout(() => {
      if (!active || epoch !== sessionEpoch.value || initialSyncDone || initialFallbackStarted) return
      initialFallbackStarted = true
      void openRoom()
    }, 300)
  }
  const isCurrent = (id: string, epoch: number) => active && epoch === sessionEpoch.value && id === selectedId.value
  function upsertGroup(item: Group) {
    groupChanges.set(item.id, ++revision)
    const index = groups.value.findIndex(g => g.id === item.id)
    // Lightweight metadata omits unread_count until live sync has counted it.
    item = { ...(index >= 0 ? groups.value[index] : {}), ...item }
    if (['join', 'invite', 'knock'].includes(item.membership)) {
      if (index < 0) groups.value.push(item); else groups.value[index] = item
    } else if (index >= 0) groups.value.splice(index, 1)
    if (selectedId.value === item.id) group.value = item
  }
  async function loadGroups(more = false) {
    if (listLoading.value || (more && !listCursor.value)) return
    const epoch = sessionEpoch.value, startedAt = revision; listLoading.value = true
    listController = new AbortController()
    try {
      const page = await groupApi.list(more ? listCursor.value! : undefined, listController.signal)
      if (!active || epoch !== sessionEpoch.value) return
      const current = new Map(groups.value.map(item => [item.id, item]))
      const items = page.groups.flatMap(item => {
        // A late list response must not resurrect a left room or overwrite a
        // newer sync update (including a real zero unread count).
        if ((groupChanges.get(item.id) ?? 0) > startedAt) return current.has(item.id) ? [current.get(item.id)!] : []
        return [{ ...current.get(item.id), ...item }]
      })
      const retained = more ? groups.value : groups.value.filter(item => (groupChanges.get(item.id) ?? 0) > startedAt)
      groups.value = [...new Map([...retained, ...items].map(item => [item.id, item])).values()]
      listCursor.value = page.next_cursor; listError.value = ''
    } catch (e) { if (active && epoch === sessionEpoch.value) listError.value = e instanceof Error ? e.message : t('group.useGroups.s1') }
    finally { if (active && epoch === sessionEpoch.value) listLoading.value = false }
  }
  function revokeInaccessible(error: unknown) {
    if (isAxiosError(error) && [403, 404].includes(error.response?.status ?? 0)) {
      messages.value = []; historyCursor.value = null
      if (group.value) upsertGroup({ ...group.value, membership: 'leave', capabilities: { ...group.value.capabilities, send_message: false } })
    }
  }
  async function refreshRoom() {
    const id = selectedId.value, epoch = sessionEpoch.value, request = version
    if (!id) return
    try { const value = await groupApi.get(id); if (isCurrent(id, epoch) && request === version) { upsertGroup(value); error.value = '' } }
    catch (e) { if (isCurrent(id, epoch) && request === version) { error.value = e instanceof Error ? e.message : t('group.useGroups.s2'); revokeInaccessible(e) } }
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
    } catch (e) { if (isCurrent(id, epoch) && request === version) { error.value = e instanceof Error ? e.message : t('group.useGroups.s3'); revokeInaccessible(e) } }
    finally { if (isCurrent(id, epoch) && request === version) loading.value = false }
  }
  async function loadHistory() {
    const id = selectedId.value, epoch = sessionEpoch.value, request = version
    if (!historyCursor.value || historyLoading.value || !id) return
    historyLoading.value = true
    try {
      const page = await groupApi.messages(id, historyCursor.value)
      if (!isCurrent(id, epoch) || request !== version) return
      messages.value = mergeGroupMessages(messages.value, page.messages, true)
      historyCursor.value = page.next_cursor; error.value = ''
    } catch (e) { if (isCurrent(id, epoch) && request === version) error.value = e instanceof Error ? e.message : t('group.useGroups.s4') }
    finally { if (isCurrent(id, epoch) && request === version) historyLoading.value = false }
  }
  async function syncLoop() {
    const epoch = sessionEpoch.value
    while (active && epoch === sessionEpoch.value) {
      if (initialSyncDone && typeof document !== 'undefined' && document.hidden) { await new Promise<void>(resolve => { hiddenWait = resolve }); continue }
      syncController = new AbortController()
      try {
        const update = await groupApi.sync(syncCursor, syncController.signal)
        if (!active || epoch !== sessionEpoch.value) return
        const initial = !initialSyncDone
        clearTimeout(initialHistoryTimer)
        syncCursor = update.next_cursor; syncError.value = ''; initialSyncDone = true
        if (update.reset) { await loadGroups() }
        if (!active || epoch !== sessionEpoch.value) return
        for (const item of update.groups ?? []) upsertGroup(item)
        for (const left of update.left_group_ids ?? []) {
          groupChanges.set(left, ++revision)
          groups.value = groups.value.filter(item => item.id !== left)
          if (group.value?.id === left) group.value = { ...group.value, membership: 'leave' }
        }
        if (update.reset) { messages.value = []; historyCursor.value = null }
        if (initial || update.reset) await openRoom()
        if (!active || epoch !== sessionEpoch.value) return
        const incoming = update.messages_by_group?.[selectedId.value]
        if (incoming) messages.value = mergeGroupMessages(messages.value, incoming)
        if (group.value && update.typing_by_group?.[group.value.id]) group.value.typing_users = update.typing_by_group[group.value.id]
        if (update.changed_group_ids?.includes(selectedId.value) && !update.groups?.some(item => item.id === selectedId.value)) await refreshRoom()
        if (!active || epoch !== sessionEpoch.value) return
        // Some servers return immediately when the snapshot changes; yield before the next long poll.
        // A degraded response means the server was busy; it keeps our cursor, so wait as asked and re-poll.
        if (!update.has_more) await new Promise<void>(resolve => { resumeWait = resolve; retryTimer = setTimeout(resolve, update.degraded ? Math.min(Math.max(update.retry_after_ms ?? 1000, 150), 10000) : 150) })
      } catch (error) {
        if (!active || epoch !== sessionEpoch.value) return
        if (isAxiosError(error) && error.response?.data?.code === 'sync_limit_exceeded') {
          syncPaused.value = true
          syncError.value = typeof error.response.data.error === 'string' ? (translateApiError(error.response.data.error) ?? error.response.data.error) : t('group.useGroups.s5')
          syncError.value += t('group.useGroups.s6')
          const needsInitialHistory = !initialSyncDone
          // A permanent sync limit must not lock the room/history UI behind a waterline that cannot arrive.
          initialSyncDone = true
          if (needsInitialHistory) await openRoom()
          return
        }
        if (isAxiosError(error) && error.response?.data?.code === 'invalid_cursor') { syncCursor = undefined; initialSyncDone = false }
        const busy = isAxiosError(error) && error.response?.data?.code === 'sync_busy'
        // A busy initial sync is retried quickly and silently; it is not a failure.
        if (!busy) syncError.value = t('group.useGroups.s7')
        // A failed live-sync endpoint must not keep a readable room behind an
        // endless initial spinner. Reconcile history again when sync recovers.
        if (!initialSyncDone && !initialFallbackStarted) { initialFallbackStarted = true; await openRoom() }
        if (!active || epoch !== sessionEpoch.value) return
        await new Promise<void>(resolve => { resumeWait = resolve; retryTimer = setTimeout(resolve, busy ? 1500 : 3000) })
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
      syncError.value = t('group.useGroups.s8')
      void syncLoop()
    } finally { if (active && epoch === sessionEpoch.value) refreshing.value = false }
  }
  function mergeMessages(incoming: GroupMessage[]) { messages.value = mergeGroupMessages(messages.value, incoming) }
  // Once initial sync has fallen back to ordinary history, every selected room
  // must remain readable even when sync_busy is retried without a visible error.
  watch(selectedId, () => { if (initialSyncDone || initialFallbackStarted) void openRoom(); else loading.value = !!selectedId.value }, { immediate: true })
  watch(sessionEpoch, () => {
    version++; syncController?.abort(); roomController?.abort(); listController?.abort(); clearTimeout(retryTimer); clearTimeout(initialHistoryTimer); resumeWait?.(); releaseHidden()
    groupChanges.clear(); revision = 0
    groups.value = []; group.value = null; messages.value = []; syncCursor = undefined; initialSyncDone = false; initialFallbackStarted = false
    listLoading.value = false; loading.value = false; historyLoading.value = false; listCursor.value = null; historyCursor.value = null
    listError.value = ''; error.value = ''; syncError.value = ''; syncPaused.value = false; refreshing.value = false
    scheduleInitialHistory(); void loadGroups(); void syncLoop()
  })
  scheduleInitialHistory(); void loadGroups(); void syncLoop()
  onBeforeUnmount(() => { active = false; version++; syncController?.abort(); roomController?.abort(); listController?.abort(); clearTimeout(retryTimer); clearTimeout(initialHistoryTimer); resumeWait?.(); releaseHidden(); document.removeEventListener('visibilitychange', onVisibility) })
  return { groups, group, messages, listCursor, historyCursor, loading, listLoading, historyLoading, error, listError, syncError, syncPaused, refreshing, refreshAfterPause, canSend, loadGroups, refreshRoom, openRoom, loadHistory, upsertGroup, mergeMessages }
}
