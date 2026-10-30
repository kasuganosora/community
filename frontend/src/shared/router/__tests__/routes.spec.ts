import { describe, it, expect } from 'vitest'
import { createMemoryHistory, createRouter, type RouteRecordRaw } from 'vue-router'
import { pageTitle, RouteNames, routes } from '../routes'

describe('pageTitle', () => {
  it('uses the Chinese page title with the app name', () => {
    expect(pageTitle('lists')).toBe('列表 - Community')
    expect(pageTitle(undefined)).toBe('Community')
    expect(pageTitle('not-a-route')).toBe('Community')
  })

  it('uses a configured instance title and falls back when that title is blank', () => {
    expect(pageTitle('home', 'メイド🎀')).toBe('首页 - メイド🎀')
    expect(pageTitle('explore', 'メイド🎀')).toBe('探索 - メイド🎀')
    expect(pageTitle('notifications', 'メイド🎀')).toBe('通知 - メイド🎀')
    expect(pageTitle(undefined, 'メイド🎀')).toBe('メイド🎀')
    expect(pageTitle('not-a-route', 'メイド🎀')).toBe('メイド🎀')
    expect(pageTitle('home', '   ')).toBe('首页 - Community')
    expect(pageTitle(undefined, '')).toBe('Community')
  })
})

describe('routes', () => {
  it('reuses the same page wrapper for routes which previously shared one page instance', () => {
    const router = createRouter({ history: createMemoryHistory(), routes })
    const view = (path: string) => router.resolve(path).matched.at(-1)?.components?.default
    expect(view('/groups')).toBe(view('/groups/room'))
    expect(view('/@alice/followers')).toBe(view('/@alice/following'))
    expect(view('/admin/settings/general')).toBe(view('/admin/settings/storage'))
    expect(view('/admin/decorations')).toBe(view('/admin/advertisements'))
  })
  it('gives groups their own authenticated routes and page titles', () => {
    const router = createRouter({ history: createMemoryHistory(), routes })
    const groups = router.resolve('/groups')
    expect(groups.name).toBe(RouteNames.GROUPS)
    expect(groups.meta.requiresAuth).toBe(true)
    expect(pageTitle(groups.name)).toBe('群组 - Community')
    const room = router.resolve('/groups/' + encodeURIComponent('!room:example.org'))
    expect(room.name).toBe(RouteNames.GROUP)
    expect(room.params.groupId).toBe('!room:example.org')
    expect(room.meta.requiresAuth).toBe(true)
    expect(router.resolve('/messages/1').name).toBe(RouteNames.CHAT_CONVERSATION)
  })
  it('redirects bookmarked group URLs without losing query, fragment, or opaque room IDs', async () => {
    const page = { template: '<div />' }
    const main = routes.find(route => route.children)?.children ?? []
    const groupRoutes = main
      .filter(route => route.path === 'groups' || route.path.startsWith('groups/') || route.path.startsWith('messages/groups'))
      .map<RouteRecordRaw>(route => route.redirect ? route : { path: route.path, name: route.name, component: page })
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: page, children: groupRoutes }] })

    await router.push('/messages/groups?action=create#details')
    expect(router.currentRoute.value.fullPath).toBe('/groups?action=create#details')

    const groupId = '!opaque/room+%?#:example.org'
    await router.push(`/messages/groups/${encodeURIComponent(groupId)}?via=example.org&via=other.org#event`)
    const current = router.currentRoute.value
    expect(current.name).toBe(RouteNames.GROUP)
    expect(current.params.groupId).toBe(groupId)
    expect(current.query.via).toEqual(['example.org', 'other.org'])
    expect(current.hash).toBe('#event')
    expect(router.resolve(current.fullPath).params.groupId).toBe(groupId)
    expect(current.path.startsWith('/groups/')).toBe(true)
  })
  it('isolates administrator pages and inherits authentication and admin guards', () => {
    const router = createRouter({ history: createMemoryHistory(), routes })
    for (const path of ['', '/accounts', '/reports', '/appeals', '/emojis', '/federation', '/system', '/settings', '/migrations']) {
      const resolved = router.resolve('/admin' + path)
      expect(resolved.meta).toMatchObject({ requiresAuth: true, requiresAdmin: true })
      expect(resolved.matched[0]?.path).toBe('/admin')
      expect(resolved.name).not.toBe('not-found')
    }
  })
  it('keeps the suspended-account appeal page signed-in but outside the admin shell', () => {
    const router = createRouter({ history: createMemoryHistory(), routes })
    const appeal = router.resolve('/appeal')
    expect(appeal.name).toBe(RouteNames.APPEAL)
    expect(appeal.meta).toMatchObject({ requiresAuth: true })
    expect(appeal.meta.requiresAdmin).not.toBe(true)
    expect(pageTitle(appeal.name)).toBe('账号申诉 - Community')
    expect(router.resolve('/admin/appeals').meta.adminPermission).toBe('reports')
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
    expect(router.resolve('/federated').matched[0]?.redirect).toEqual({ name: 'explore', query: { tab: 'federated' } })
    expect(router.resolve('/timeline/public').matched[0]?.redirect).toEqual({ name: 'explore', query: { tab: 'federated' } })
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
