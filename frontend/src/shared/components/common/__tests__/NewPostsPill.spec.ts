import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import NewPostsPill from '../NewPostsPill.vue'

function deferred<T = void>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

describe('NewPostsPill', () => {
  it('shows loading feedback immediately and ignores clicks until the refresh settles', async () => {
    const pending = deferred()
    let calls = 0
    const wrapper = mount(NewPostsPill, {
      props: { show: true, reveal: () => { calls += 1; return pending.promise } },
    })
    const button = wrapper.get('[data-testid="new-posts-pill"]')
    expect(button.text()).toContain('有新帖子，点击查看')
    expect(button.attributes('disabled')).toBeUndefined()
    expect(wrapper.find('.app-loading').exists()).toBe(false)

    await button.trigger('click')
    expect(calls).toBe(1)
    expect(button.attributes('disabled')).toBeDefined()
    expect(button.attributes('aria-busy')).toBe('true')
    expect(button.text()).toContain('加载中…')
    expect(wrapper.find('.app-loading').exists()).toBe(true)
    expect(button.text()).not.toContain('有新帖子，点击查看')

    await button.trigger('click')
    expect(calls).toBe(1)

    pending.resolve()
    await flushPromises()
    expect(button.attributes('disabled')).toBeUndefined()
    expect(button.attributes('aria-busy')).toBeUndefined()
    expect(button.text()).toContain('有新帖子，点击查看')
    expect(wrapper.find('.app-loading').exists()).toBe(false)
  })

  it('does not show a loading state when reveal finishes synchronously', async () => {
    let calls = 0
    const wrapper = mount(NewPostsPill, {
      props: { show: true, reveal: () => { calls += 1 } },
    })
    const button = wrapper.get('[data-testid="new-posts-pill"]')
    await button.trigger('click')
    expect(calls).toBe(1)
    expect(button.attributes('disabled')).toBeUndefined()
    expect(button.attributes('aria-busy')).toBeUndefined()
    expect(button.text()).toContain('有新帖子，点击查看')
    expect(wrapper.find('.app-loading').exists()).toBe(false)
  })

  it('re-enables the pill when refreshing fails', async () => {
    const pending = deferred()
    const wrapper = mount(NewPostsPill, {
      props: { show: true, reveal: () => pending.promise },
    })
    const button = wrapper.get('[data-testid="new-posts-pill"]')
    await button.trigger('click')
    expect(button.attributes('disabled')).toBeDefined()
    expect(button.text()).toContain('加载中…')

    pending.reject(new Error('offline'))
    await flushPromises()
    expect(button.attributes('disabled')).toBeUndefined()
    expect(button.attributes('aria-busy')).toBeUndefined()
    expect(button.text()).toContain('有新帖子，点击查看')
    expect(wrapper.find('.app-loading').exists()).toBe(false)
  })
})
