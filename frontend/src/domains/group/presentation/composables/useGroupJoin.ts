import { onBeforeUnmount, ref, watch, type Ref } from 'vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { groupApi } from '../../infrastructure/groupApi'
import { isRetryableGroupRequestError } from '../../infrastructure/groupErrors'
import type { Group, GroupJoinResult } from '../../domain/group'

import { t } from '@/shared/i18n'
// Accepting an invitation uses the same bounded, resumable operation as joining
// from search. A retry must query an existing operation, not submit another join.
// A slow or briefly unavailable server (gateway timeout, 5xx, 429, dropped
// connection) is not a join failure: the operation keeps running server-side.
export function useGroupJoin(roomId: Ref<string>, apply: (result: GroupJoinResult) => void) {
  const busy = ref(false), error = ref(''), status = ref(''), operation = ref('')
  let generation = 0, controller: AbortController | undefined
  let pollTimer: ReturnType<typeof setTimeout> | undefined, deadline: ReturnType<typeof setTimeout> | undefined
  function stop() { generation++; controller?.abort(); clearTimeout(pollTimer); clearTimeout(deadline); busy.value = false }
  function reset() { stop(); error.value = ''; status.value = ''; operation.value = '' }
  watch([roomId, sessionEpoch], reset, { flush: 'sync' })
  onBeforeUnmount(stop)
  async function join(group: Group) {
    if (busy.value) return
    stop()
    const version = generation
    controller = new AbortController()
    const signal = controller.signal
    busy.value = true; error.value = ''; status.value = t('group.useGroupJoin.s1')
    const fail = (message: string) => { if (version !== generation) return; stop(); status.value = ''; error.value = message }
    deadline = setTimeout(() => fail(t('group.useGroupJoin.s2')), 120000)
    async function handle(result: GroupJoinResult) {
      if (version !== generation) return
      if (result.status === 'joining') {
        operation.value = result.operation_id || operation.value
        if (!operation.value) { fail(t('group.useGroupJoin.s3')); return }
        pollTimer = setTimeout(() => { void query() }, 1500)
        return
      }
      stop(); operation.value = ''
      status.value = result.status === 'knock' ? t('group.useGroupJoin.s4') : t('group.useGroupJoin.s5')
      apply(result)
    }
    async function query() {
      if (version !== generation) return
      try { await handle(await groupApi.joinStatus(operation.value, signal)) }
      catch (e) {
        if (version !== generation) return
        if (isRetryableGroupRequestError(e)) { status.value = t('group.useGroupJoin.s6'); pollTimer = setTimeout(() => { void query() }, 3000); return }
        fail(e instanceof Error ? e.message : t('group.useGroupJoin.s7'))
      }
    }
    if (operation.value) { await query(); return }
    try {
      await handle(await groupApi.join({ target: group.target || group.id, via: group.via ?? [], action: group.membership !== 'invite' && group.join_rule?.startsWith('knock') ? 'knock' : 'join' }, signal))
    } catch (e) { fail(e instanceof Error ? e.message : t('group.useGroupJoin.s8')) }
  }
  return { busy, error, status, operation, join }
}
