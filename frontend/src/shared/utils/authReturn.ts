/** A same-origin path that is safe to send the browser to after login. */
export function safeInternalPath(value: unknown): string | null {
  if (typeof value !== 'string' || !value.startsWith('/') || value.startsWith('//') || value.startsWith('/\\')) {
    return null
  }
  return value
}

/**
 * OAuth, MiAuth and legacy Misskey authorization pages are served by the API,
 * not the Vue router. After login the browser must do a full navigation so the
 * server can render the consent page and then the client's callback.
 */
export function isServerAuthReturn(path: string): boolean {
  return path.startsWith('/oauth/') || path.startsWith('/miauth/') || path.startsWith('/auth/')
}
