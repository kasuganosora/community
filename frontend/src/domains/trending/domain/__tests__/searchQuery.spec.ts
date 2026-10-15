import { expect, it } from 'vitest'
import { wantsResolve } from '../searchQuery'

it('resolves only handles and links', () => {
  for (const q of ['@alice@example.social', 'alice@example.social', 'https://example.social/@alice/1', ' http://a.b/c ']) expect(wantsResolve(q), q).toBe(true)
  for (const q of ['跳蛋', 'godot engine', '@alice', '#tag', 'a@b', 'mail me@x']) expect(wantsResolve(q), q).toBe(false)
})
