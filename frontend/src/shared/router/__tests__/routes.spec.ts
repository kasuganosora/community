import { describe, it, expect } from 'vitest'
import { pageTitle, routes } from '../routes'

describe('pageTitle', () => {
  it('uses the Chinese page title with the app name', () => {
    expect(pageTitle('lists')).toBe('列表 - Community')
    expect(pageTitle(undefined)).toBe('Community')
    expect(pageTitle('not-a-route')).toBe('Community')
  })
})

describe('routes', () => {
  it('registers the pages linked from the UI', () => {
    const main = routes.find(r => r.children)?.children?.map(c => c.path) ?? []
    for (const p of ['tags/:tag', '@:username/followers', '@:username/following', 'lists', 'lists/:id', 'settings']) {
      expect(main).toContain(p)
    }
  })
})
