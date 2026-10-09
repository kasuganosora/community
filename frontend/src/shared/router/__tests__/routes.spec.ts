import { describe, it, expect } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { pageTitle, routes } from '../routes'

describe('pageTitle', () => {
  it('uses the Chinese page title with the app name', () => {
    expect(pageTitle('lists')).toBe('列表 - Community')
    expect(pageTitle(undefined)).toBe('Community')
    expect(pageTitle('not-a-route')).toBe('Community')
  })
})

describe('routes', () => {
  it('isolates administrator pages and inherits authentication and admin guards', () => {
    const router = createRouter({ history: createMemoryHistory(), routes })
    for (const path of ['', '/accounts', '/reports', '/emojis', '/federation', '/system', '/settings', '/migrations']) {
      const resolved = router.resolve('/admin' + path)
      expect(resolved.meta).toMatchObject({ requiresAuth: true, requiresAdmin: true })
      expect(resolved.matched[0]?.path).toBe('/admin')
      expect(resolved.name).not.toBe('not-found')
    }
  })
  it('separates administrator site migration from personal data tools', () => {
    const router = createRouter({ history: createMemoryHistory(), routes })
    expect(router.resolve('/admin/migrations').meta).toMatchObject({ requiresAuth: true, requiresAdmin: true })
    expect(router.resolve('/settings/data').meta.requiresAuth).toBe(true)
    expect(router.resolve('/settings/data').meta.requiresAdmin).not.toBe(true)
  })
  it('marks emoji administration as authenticated and administrator-only', () => {
    const router = createRouter({ history: createMemoryHistory(), routes })
    const route = router.resolve('/admin/emojis')
    expect(route.meta.requiresAuth).toBe(true)
    expect(route.meta.requiresAdmin).toBe(true)
    expect(pageTitle(route.name)).toBe('自定义表情管理 - Community')
  })
  it('opens the public post URLs published by the API and federation', () => {
    const router = createRouter({ history: createMemoryHistory(), routes })
    const canonical = router.resolve('/@alice/status/42')
    const published = router.resolve('/@alice/42')
    expect(published.name).toBe(canonical.name)
    expect(published.params).toEqual({ username: 'alice', statusId: '42' })
    expect(router.resolve('/@alice/followers').name).not.toBe(canonical.name)
  })
  it('registers the pages linked from the UI', () => {
    const main = routes.find(r => r.children)?.children?.map(c => c.path) ?? []
    for (const p of ['tags/:tag', '@:username/followers', '@:username/following', 'lists', 'lists/:id', 'settings']) {
      expect(main).toContain(p)
    }
  })
  it('maps Mastodon web timeline paths to explore', () => {
    const router = createRouter({ history: createMemoryHistory(), routes })
    expect(router.resolve('/public').matched[0]?.redirect).toEqual({ name: 'explore', query: { tab: 'federated' } })
    expect(router.resolve('/public/local').matched[0]?.redirect).toEqual({ name: 'explore', query: { tab: 'local' } })
    expect(router.resolve('/public/nope').name).toBe('not-found')
  })
  it('round-trips a login redirect that carries an encoded query', () => {
    const router = createRouter({ history: createMemoryHistory(), routes })
    const target = router.resolve('/search?q=%23test')
    expect(target.query.q).toBe('#test')
    const landing = router.resolve({ name: 'landing', query: { redirect: target.fullPath } })
    // One level of encoding for the embedded "%": decoding once gives the target back.
    expect(landing.fullPath).toBe('/?redirect=/search?q%3D%2523test'.replace('%3D', '='))
    const back = router.resolve(landing.fullPath).query.redirect as string
    expect(back).toBe('/search?q=%23test')
    expect(router.resolve(back).query.q).toBe('#test')
  })
})
