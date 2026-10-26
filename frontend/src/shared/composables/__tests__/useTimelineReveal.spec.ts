/* Multiple components form one test harness. */
/* eslint-disable vue/one-component-per-file */
import { defineComponent, h, ref } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { useTimelineReveal } from '../useTimelineReveal'

function setup(load: () => boolean | Promise<boolean>, error: () => string | null = () => null) {
  const scope = ref('home')
  const revealHead = vi.fn().mockResolvedValue(undefined)
  const Target = defineComponent({ setup(_, { expose }) { expose({ revealHead }); return () => h('div') } })
  const Host = defineComponent({
    setup() {
      const { reveal } = useTimelineReveal(load, () => scope.value, error)
      return { reveal }
    },
    render() { return h('div', [h('header', { ref: 'timelineHeader' }), h(Target, { ref: 'timelineView' })]) },
  })
  return { wrapper: mount(Host), scope, revealHead }
}

describe('new-post navigation', () => {
  it('waits for a successful commit before moving to the new head', async () => {
    let finish!: (result: boolean) => void
    const { wrapper, revealHead } = setup(() => new Promise(resolve => { finish = resolve }))
    const pending = wrapper.vm.reveal()
    expect(revealHead).not.toHaveBeenCalled()
    finish(true); await pending
    expect(revealHead).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
  it('keeps the reading position when the request fails', async () => {
    const { wrapper, revealHead } = setup(() => Promise.resolve(false), () => 'offline')
    await expect(wrapper.vm.reveal()).rejects.toThrow('offline')
    expect(revealHead).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it.each(['scope', 'unmount'])('ignores completion after a %s change', async action => {
    let finish!: (result: boolean) => void
    const { wrapper, scope, revealHead } = setup(() => new Promise(resolve => { finish = resolve }))
    const pending = wrapper.vm.reveal()
    if (action === 'scope') scope.value = 'public'
    else wrapper.unmount()
    finish(true); await pending
    expect(revealHead).not.toHaveBeenCalled()
    if (action === 'scope') wrapper.unmount()
  })
  it('does not report a network wait for a ready head', async () => {
    const { wrapper, revealHead } = setup(() => true)
    expect(wrapper.vm.reveal()).toBeUndefined()
    await flushPromises()
    expect(revealHead).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
})
