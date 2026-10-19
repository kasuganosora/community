/** Personal access token permissions, ordered as view/edit pairs. */
export interface ScopeRow { scope: string }

export const userScopeRows: ScopeRow[] = [
  'read:accounts', 'write:accounts',
  'read:blocks', 'write:blocks',
  'read:bookmarks', 'write:bookmarks',
  'read:favourites', 'write:favourites',
  'read:filters', 'write:filters',
  'read:follows', 'write:follows',
  'read:lists', 'write:lists',
  'read:mutes', 'write:mutes',
  'read:notifications', 'write:notifications',
  'read:statuses', 'write:statuses',
  'read:search', 'write:media', 'write:reports', 'write:conversations', 'push',
].map(scope => ({ scope }))

/** Only admin scopes that guard real endpoints on this server. */
export const adminScopeRows: ScopeRow[] = [
  'admin:read', 'admin:write',
  'admin:read:accounts', 'admin:write:accounts',
  'admin:read:reports', 'admin:write:reports',
].map(scope => ({ scope }))

export const scopeLabelKey = (scope: string) => `settings.Connections.scopeLabels.${scope.replace(/:/g, '__')}`

/** Collapses a full set of granular read/write scopes to the parent scope. */
export function collapseScopes(selected: Iterable<string>): string[] {
  const set = new Set(selected)
  for (const parent of ['read', 'write']) {
    const children = userScopeRows.map(r => r.scope).filter(s => s.startsWith(`${parent}:`))
    if (children.every(s => set.has(s))) { children.forEach(s => set.delete(s)); set.add(parent) }
  }
  return [...set].sort()
}

export const needsReauth = (scopes: string[]) => scopes.some(s => s === 'admin:write' || s.startsWith('admin:write:'))
