import { ref } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { describe, expect, it } from 'vitest'
import { trackNavigation } from '../navigationFeedback'

function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>(done => { resolve = done })
  return { promise, resolve }
}
function setup() {
  const page = { template: '<div />' }
  const router = createRouter({ history: createMemoryHistory(), routes: ['/', '/first', '/second', '/redirect', '/error'].map(path => ({ path, component: page })) })
  const pending = ref(false)
  const stop = trackNavigation(router, pending)
  return { router, pending, stop }
}

describe('navigation feedback', () => {
  it('keeps feedback while permissions are checked, without advancing the route', async () => {
    const { router, pending, stop } = setup()
    await router.push('/')
    const gate = deferred(), started = deferred()
    router.beforeEach(async to => { if (to.path === '/first') { started.resolve(); await gate.promise; return false } })
    const visit = router.push('/first')
    await started.promise
    expect(pending.value).toBe(true)
    expect(router.currentRoute.value.path).toBe('/')
    gate.resolve()
    await visit
    expect(pending.value).toBe(false)
    expect(router.currentRoute.value.path).toBe('/')
    stop()
  })

  it('does not let a cancelled older navigation hide feedback for a newer request', async () => {
    const { router, pending, stop } = setup()
    await router.push('/')
    const first = deferred(), second = deferred(), firstStarted = deferred(), secondStarted = deferred()
    router.beforeEach(async to => {
      if (to.path === '/first') { firstStarted.resolve(); await first.promise }
      if (to.path === '/second') { secondStarted.resolve(); await second.promise }
    })
    const old = router.push('/first')
    await firstStarted.promise
    const current = router.push('/second')
    await secondStarted.promise
    first.resolve()
    await old
    expect(pending.value).toBe(true)
    second.resolve()
    await current
    expect(pending.value).toBe(false)
    stop()
  })

  it('clears progress after redirects, duplicate visits and rejected guards', async () => {
    const { router, pending, stop } = setup()
    router.beforeEach(to => { if (to.path === '/redirect') return '/second'; if (to.path === '/error') throw new Error('failed') })
    await router.push('/redirect')
    expect(router.currentRoute.value.path).toBe('/second')
    expect(pending.value).toBe(false)
    await router.push('/second')
    expect(pending.value).toBe(false)
    await expect(router.push('/error')).rejects.toThrow('failed')
    expect(pending.value).toBe(false)
    stop()
  })

  it('stops an older pending indication when the current page is selected again', async () => {
    const { router, pending, stop } = setup()
    await router.push('/')
    const gate = deferred(), started = deferred()
    router.beforeEach(async to => { if (to.path === '/first') { started.resolve(); await gate.promise } })
    const old = router.push('/first')
    await started.promise
    expect(pending.value).toBe(true)
    await router.push('/')
    expect(pending.value).toBe(false)
    gate.resolve()
    await old
    expect(router.currentRoute.value.path).toBe('/')
    expect(pending.value).toBe(false)
    stop()
  })
})
