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
})
