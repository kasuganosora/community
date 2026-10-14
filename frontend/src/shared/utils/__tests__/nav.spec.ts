import { describe, it, expect } from 'vitest'
import { activeNavSection } from '../nav'

describe('activeNavSection', () => {
  it('highlights the profile only on your own profile pages', () => {
    expect(activeNavSection('/@alice', 'alice')).toBe('profile')
    expect(activeNavSection('/@alice/followers', 'alice')).toBe('profile')
    expect(activeNavSection('/@alice/status/1', 'alice')).toBe('')
    expect(activeNavSection('/@alicex', 'alice')).toBe('')
    expect(activeNavSection('/@bob', 'alice')).toBe('')
  })
  it('maps sections by whole segments', () => {
    expect(activeNavSection('/', 'alice')).toBe('home')
    expect(activeNavSection('/lists/3', 'alice')).toBe('lists')
    expect(activeNavSection('/listsx', 'alice')).toBe('')
    expect(activeNavSection('/search', 'alice')).toBe('explore')
    expect(activeNavSection('/tags/intro', 'alice')).toBe('explore')
    expect(activeNavSection('/messages', 'alice')).toBe('messages')
    expect(activeNavSection('/groups', 'alice')).toBe('groups')
    expect(activeNavSection('/groups/room-id', 'alice')).toBe('groups')
    expect(activeNavSection('/groups-other', 'alice')).toBe('')
    // Bookmarked legacy URLs retain their section while the router redirects.
    expect(activeNavSection('/messages/groups', 'alice')).toBe('groups')
    expect(activeNavSection('/messages/groups/room-id', 'alice')).toBe('groups')
    expect(activeNavSection('/messages/groups-other', 'alice')).toBe('messages')
  })
})
