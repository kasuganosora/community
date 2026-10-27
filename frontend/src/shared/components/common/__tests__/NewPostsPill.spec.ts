import { defineComponent, h } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import NewPostsPill from '../NewPostsPill.vue'

function box(partial: Partial<DOMRect>): DOMRect {
  return { x: 0, y: 0, width: 0, height: 0, top: 0, right: 0, bottom: 0, left: 0, toJSON() { return this }, ...partial } as DOMRect
}

function deferred<T = void>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

describe('NewPostsPill', () => {
  afterEach(() => vi.restoreAllMocks())

  it.each([-500, 200])('preserves reading position only when the changing row is above the viewport (%i)', async top => {
    let scroll = 800
    const scrollTo = vi.spyOn(window, 'scrollTo').mockImplementation((options: ScrollToOptions | number) => {
      if (typeof options === 'object') scroll = options.top ?? scroll
    })
    vi.spyOn(window, 'scrollY', 'get').mockImplementation(() => scroll)
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      return { top, height: this.querySelector('button') ? 56 : 0 } as DOMRect
    })
    const wrapper = mount(NewPostsPill, { props: { show: false, layout: 'row', reveal: () => {} } })
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(scroll).toBe(top < 0 ? 856 : 800)
    await wrapper.setProps({ show: false })
    await flushPromises()
    expect(scroll).toBe(800)
    expect(scrollTo).toHaveBeenCalledTimes(top < 0 ? 2 : 0)
    wrapper.unmount()
  })

  it.each([
    ['under an overlapping sticky header', 40, true, true],
    ['just below that header', 160, true, false],
    ['flush with the header bottom', 120, true, false],
    ['beside a full-height sticky sidebar', 40, false, false],
  ])('preserves reading position when the row changes %s', async (_label, top, header, compensate) => {
    let scroll = 800
    const scrollTo = vi.spyOn(window, 'scrollTo').mockImplementation((options: ScrollToOptions | number) => {
      if (typeof options === 'object') scroll = options.top ?? scroll
    })
    vi.spyOn(window, 'scrollY', 'get').mockImplementation(() => scroll)
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      if (this.classList.contains('sidebar')) return box({ top: 0, bottom: 900, left: -220, right: 0, width: 220, height: 900 })
      if (this.classList.contains('sticky-header')) return box({ top: 0, bottom: 120, left: 0, right: 400, width: 400, height: 120 })
      const open = !!this.querySelector('button')
      return box({ top, bottom: top + (open ? 56 : 0), left: 16, right: 384, width: 368, height: open ? 56 : 0 })
    })
    const Host = defineComponent({
      name: 'RowHost',
      props: { show: { type: Boolean, required: true }, header: { type: Boolean, required: true } },
      setup(props) {
        return () => h('div', [
          h('div', { class: 'sidebar', style: { position: 'sticky' } }),
          h('div', [
            props.header ? h('div', { class: 'sticky-header', style: { position: 'sticky' } }) : null,
            h(NewPostsPill, { layout: 'row', show: props.show, reveal: () => {} }),
          ]),
        ])
      },
    })
    const wrapper = mount(Host, { props: { show: false, header } })
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(scroll).toBe(compensate ? 856 : 800)
    await wrapper.setProps({ show: false })
    await flushPromises()
    expect(scroll).toBe(800)
    expect(scrollTo).toHaveBeenCalledTimes(compensate ? 2 : 0)
    wrapper.unmount()
  })

  it('does not move the viewport for a header action', async () => {
    const scrollTo = vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
    vi.spyOn(window, 'scrollY', 'get').mockReturnValue(800)
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      return box({ top: -500, height: this.querySelector('button') ? 56 : 0, bottom: this.querySelector('button') ? -444 : -500 })
    })
    const wrapper = mount(NewPostsPill, { props: { show: false, reveal: () => {} } })
    await wrapper.setProps({ show: true })
    await flushPromises()
    await wrapper.setProps({ show: false })
    await flushPromises()
    expect(scrollTo).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('collapses a revealed row before the navigation callback finishes, without a second layout shift', async () => {
    const pending = deferred()
    const wrapper = mount(NewPostsPill, { props: { show: true, layout: 'row', reveal: () => pending.promise } })
    await wrapper.get('button').trigger('click')
    expect(wrapper.get('button').attributes('aria-busy')).toBe('true')
    await wrapper.setProps({ show: false })
    expect(wrapper.find('button').exists()).toBe(false)
    pending.resolve()
    await flushPromises()
    expect(wrapper.find('button').exists()).toBe(false)
    wrapper.unmount()
  })

  it('shows loading feedback immediately and ignores clicks until the refresh settles', async () => {
    const pending = deferred()
    let calls = 0
    const wrapper = mount(NewPostsPill, {
      props: { show: true, reveal: () => { calls += 1; return pending.promise } },
    })
    const button = wrapper.get('[data-testid="new-posts-pill"]')
    expect(button.text()).toContain('查看新帖')
    expect(button.attributes('disabled')).toBeUndefined()
    expect(wrapper.find('.app-loading').exists()).toBe(false)

    await button.trigger('click')
    expect(calls).toBe(1)
    expect(button.attributes('disabled')).toBeDefined()
    expect(button.attributes('aria-busy')).toBe('true')
    expect(button.text()).toContain('加载中…')
    expect(wrapper.find('.app-loading').exists()).toBe(true)
    expect(button.text()).not.toContain('查看新帖')

    await button.trigger('click')
    expect(calls).toBe(1)

    pending.resolve()
    await flushPromises()
    expect(button.attributes('disabled')).toBeUndefined()
    expect(button.attributes('aria-busy')).toBeUndefined()
    expect(button.text()).toContain('查看新帖')
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
    expect(button.text()).toContain('查看新帖')
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
    expect(button.text()).toContain('重试加载')
    expect(wrapper.find('.app-loading').exists()).toBe(false)
  })
})
