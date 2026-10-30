/** A suspended account kept its bearer token so it can still call /appeals. */

let active = false
let signingOut = false
const listeners = new Set<() => void>()

export function isAppealSession(): boolean {
  return active
}

export function isLocalSignOut(): boolean {
  return signingOut
}

export function onAppealSession(cb: () => void): void {
  listeners.add(cb)
}

export function markAppealSession(): void {
  const first = !active
  active = true
  if (!first) return
  for (const cb of listeners) {
    try { cb() } catch (e) { console.error(e) }
  }
}

export function clearAppealSession(): void {
  active = false
}

/** Skip the suspension probe while this tab is deliberately dropping the session. */
export function beginLocalSignOut(): void {
  signingOut = true
  clearAppealSession()
}

export function endLocalSignOut(): void {
  signingOut = false
}
