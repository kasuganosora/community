/* eslint-disable vue/one-component-per-file -- Separate synthetic route fixtures are intentional. */
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { createMemoryHistory, createRouter, onBeforeRouteLeave, onBeforeRouteUpdate, RouterView, type RouteRecordRaw } from 'vue-router'
import { describe, expect, it, vi } from 'vitest'
import { lazyRoute, preloadRouteViews } from '../lazyRoute'

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: Error) => void
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail })
  return { promise, resolve, reject }
}
const previous = { template: '<p>原来的页面</p>' }
const result = { default: defineComponent({ template: '<p>结果页面</p>' }) }
async function setup(routes: RouteRecordRaw[]) {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: previous }, ...routes] })
  router.beforeResolve(preloadRouteViews)
  await router.push('/')
  const wrapper = mount(RouterView, { global: { plugins: [router] } })
  return { router, wrapper }
}

describe('lazy route feedback', () => {
  it('commits navigation while code is pending and reuses loaded pages without another placeholder', async () => {
    const module = deferred<typeof result>()
    const loader = vi.fn(() => module.promise)
    const { router, wrapper } = await setup([{ path: '/search', name: 'search', component: lazyRoute(loader, () => '搜索') }])
    await router.push('/search?q=hello')
    expect(router.currentRoute.value.path).toBe('/search')
    expect(wrapper.find('[role="status"][aria-label="正在加载搜索"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('原来的页面')
    module.resolve(result)
    await flushPromises()
    expect(wrapper.text()).toContain('结果页面')
    await router.push('/')
    await router.push('/search?q=again')
    expect(wrapper.text()).toContain('结果页面')
    expect(wrapper.find('[role="status"]').exists()).toBe(false)
    expect(loader).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('forwards route props and keeps page composition update/leave guards active', async () => {
    const leave = vi.fn(() => false)
    const update = vi.fn()
    const page = defineComponent({ props: { id: String }, setup(props) {
      onBeforeRouteLeave(leave)
      onBeforeRouteUpdate(update)
      return () => h('p', props.id)
    } })
    const { router, wrapper } = await setup([{ path: '/room/:id', props: true, component: lazyRoute(async () => ({ default: page })) }])
    await router.push('/room/first')
    await flushPromises()
    expect(wrapper.text()).toBe('first')
    await router.push('/room/second')
    expect(update).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toBe('second')
    await router.push('/')
    expect(leave).toHaveBeenCalledTimes(1)
    expect(router.currentRoute.value.path).toBe('/room/second')
    expect(wrapper.text()).toBe('second')
    leave.mockReturnValue(true)
    await router.push('/')
    expect(wrapper.text()).toBe('原来的页面')
    wrapper.unmount()
  })

  it('starts layout and child imports together only after access guards allow navigation', async () => {
    const parent = deferred<{ default: ReturnType<typeof defineComponent> }>()
    const child = deferred<typeof result>()
    const layoutLoader = vi.fn(() => parent.promise)
    const pageLoader = vi.fn(() => child.promise)
    const { router, wrapper } = await setup([{ path: '/admin', component: lazyRoute(layoutLoader), children: [{ path: 'settings', component: lazyRoute(pageLoader) }] }])
    let allowed = false
    router.beforeEach(to => to.path.startsWith('/admin') && !allowed ? false : undefined)
    await router.push('/admin/settings')
    expect(layoutLoader).not.toHaveBeenCalled()
    expect(pageLoader).not.toHaveBeenCalled()
    allowed = true
    await router.push('/admin/settings')
    expect(layoutLoader).toHaveBeenCalledTimes(1)
    expect(pageLoader).toHaveBeenCalledTimes(1)
    parent.resolve({ default: defineComponent({ setup: () => () => h(RouterView) }) })
    child.resolve(result)
    await flushPromises()
    expect(wrapper.text()).toBe('结果页面')
    wrapper.unmount()
  })

  it('ignores a page finishing after the user has left', async () => {
    const module = deferred<typeof result>()
    const { router, wrapper } = await setup([{ path: '/slow', component: lazyRoute(() => module.promise) }])
    await router.push('/slow')
    await router.push('/')
    module.resolve(result)
    await flushPromises()
    expect(wrapper.text()).toBe('原来的页面')
    wrapper.unmount()
  })

  it('shows a recovery action when the module fails and allows a later successful visit', async () => {
    const loader = vi.fn().mockRejectedValueOnce(new Error('network')).mockRejectedValueOnce(new Error('network')).mockResolvedValue(result)
    const { router, wrapper } = await setup([{ path: '/slow', component: lazyRoute(loader) }])
    await router.push('/slow')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('页面加载失败')
    expect(wrapper.get('button').text()).toBe('重新加载')
    await router.push('/')
    await router.push('/slow')
    await flushPromises()
    expect(wrapper.text()).toContain('结果页面')
    wrapper.unmount()
  })
})
