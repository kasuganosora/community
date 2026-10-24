// Page-lifetime memory of avatar srcs that have already decoded.
// Each status row mounts its own UserAvatar. The resolved src is the same
// for the same account (including the size query), but a fresh instance
// would otherwise show the grey placeholder until its own load event.

const loaded = new Set<string>()
const listeners = new Set<(src: string) => void>()

/** True when some avatar on this page has already decoded this exact src. */
export function isAvatarLoaded(src: string): boolean {
  return src !== '' && loaded.has(src)
}

/** Record a decoded avatar src and wake other avatars waiting on it. */
export function markAvatarLoaded(src: string): void {
  if (!src || loaded.has(src)) return
  loaded.add(src)
  for (const listener of [...listeners]) listener(src)
}

/** Subscribe until the returned function is called. */
export function onAvatarLoaded(listener: (src: string) => void): () => void {
  listeners.add(listener)
  return () => { listeners.delete(listener) }
}

/** Drop decoded URLs. Tests call this so cases do not share page state. */
export function resetAvatarLoadCache(): void {
  loaded.clear()
  listeners.clear()
}
