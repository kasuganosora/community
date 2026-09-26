import { describe, it, expect } from 'vitest'
import { internalRouteForLink } from '../links'

const local = ['localhost:8081']

describe('internalRouteForLink', () => {
  it('routes hashtags to the tag page (incl. non-ASCII)', () => {
    expect(internalRouteForLink('http://localhost:8081/tags/%E4%B8%AD%E6%96%87', 'mention hashtag', local))
      .toBe('/tags/%E4%B8%AD%E6%96%87')
    expect(internalRouteForLink('https://remote.social/tags/intro', 'mention hashtag', local)).toBe('/tags/intro')
  })

  it('routes local mentions to the profile', () => {
    expect(internalRouteForLink('http://localhost:8081/@alice', 'u-url mention', local)).toBe('/@alice')
  })

  it('keeps the remote domain for remote mentions', () => {
    expect(internalRouteForLink('https://remote.social/@bob', 'u-url mention', local)).toBe('/@bob@remote.social')
    expect(internalRouteForLink('https://remote.social/users/bob', 'u-url mention', local)).toBe('/@bob@remote.social')
  })

  it('leaves ordinary links alone', () => {
    expect(internalRouteForLink('https://example.org/page', '', local)).toBeNull()
    expect(internalRouteForLink('https://remote.social/about', 'mention', local)).toBeNull()
  })
})
