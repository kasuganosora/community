import { describe, expect, it } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { routes } from '@/shared/router/routes'
import { adminEntry, adminNavigation, canAccessAdmin, visibleAdminNavigation } from '../navigation'

describe('capability-scoped administrator navigation', () => {
  it('maps every menu entry to a protected implemented page with matching capabilities', () => {
    const router = createRouter({ history: createMemoryHistory(), routes })
    for (const entry of adminNavigation.flatMap(group => group.items)) {
      const route = router.resolve(entry.path)
      expect(route.name).not.toBe('not-found')
      expect(route.meta).toMatchObject({ requiresAdmin: true, requiresAuth: true })
      expect(route.meta.adminPermission).toBe(entry.permission)
      expect(!!route.meta.adminRootOnly).toBe(!!entry.rootOnly)
    }
  })
  it('allows delegated managers only their sections and chooses an accessible landing page', () => {
    const manager = { is_admin: false, admin_permissions: ['emojis', 'branding'] }
    const links = visibleAdminNavigation(manager).flatMap(g => g.items.map(i => i.path))
    expect(links).toEqual(['/admin/emojis', '/admin/decorations', '/admin/advertisements', '/admin/settings/branding'])
    expect(adminEntry(manager)).toBe('/admin/emojis')
    expect(canAccessAdmin(manager, 'users')).toBe(false)
    expect(canAccessAdmin(manager, undefined, true)).toBe(false)
    expect(adminEntry({ admin_permissions: ['unrecognized'] })).toBeUndefined()
  })
  it('fails closed for ordinary users while preserving explicit root access', () => {
    expect(visibleAdminNavigation({ is_admin: false })).toEqual([])
    expect(canAccessAdmin(null, 'dashboard')).toBe(false)
    expect(canAccessAdmin({ is_admin: true }, undefined, true)).toBe(true)
    expect(visibleAdminNavigation({ is_admin: true })).toEqual(adminNavigation)
  })
  it('keeps ordinary announcements separate from announcement management', () => {
    const router = createRouter({ history: createMemoryHistory(), routes })
    expect(router.resolve('/announcements').meta.requiresAdmin).not.toBe(true)
    expect(router.resolve('/announcements').meta.requiresAuth).toBe(true)
    expect(router.resolve('/admin/announcements').meta.adminPermission).toBe('announcements')
    expect(router.resolve('/admin/rules').meta.adminPermission).toBe('rules')
    expect(router.resolve('/admin/settings/security').meta.adminPermission).toBe('settings')
  })
})
