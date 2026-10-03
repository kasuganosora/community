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
