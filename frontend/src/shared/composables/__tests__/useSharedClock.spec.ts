import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { mount } from '@vue/test-utils'
import { CLOCK_INTERVAL_MS, useSharedClock } from '../useSharedClock'

let hidden = false
beforeEach(() => { vi.useFakeTimers(); hidden = false; vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden) })
afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers() })

describe('useSharedClock', () => {
  it('ticks once a minute, stops while hidden and catches up on return', () => {
    let now!: ReturnType<typeof useSharedClock>
    const wrapper = mount(defineComponent({ setup() { now = useSharedClock(); return () => h('i') } }))
    expect(CLOCK_INTERVAL_MS).toBe(60_000)
    const start = now.value
    vi.advanceTimersByTime(30_000); expect(now.value).toBe(start)
    vi.advanceTimersByTime(30_000); expect(now.value).toBeGreaterThan(start)
    hidden = true; document.dispatchEvent(new Event('visibilitychange'))
    expect(vi.getTimerCount()).toBe(0)
    const paused = now.value
    vi.advanceTimersByTime(5 * 60_000); expect(now.value).toBe(paused)
    hidden = false; document.dispatchEvent(new Event('visibilitychange'))
    expect(now.value).toBeGreaterThan(paused)
    expect(vi.getTimerCount()).toBe(1)
    wrapper.unmount()
    expect(vi.getTimerCount()).toBe(0)
  })
})
