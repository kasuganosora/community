import { describe, it, expect } from 'vitest'
import { internalRouteForLink, shortenUrlText, shortenBareLinks } from '../links'

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

describe('shortenUrlText', () => {
  it('strips protocol and www and truncates long URLs', () => {
    const url = 'https://www.github.com/kasuganosora/community/blob/master/README.md'
    expect(shortenUrlText(url, url)).toBe('github.com/kasuganosora/commun…')
  })

  it('keeps short URLs whole without protocol', () => {
    expect(shortenUrlText('http://example.com/a', 'http://example.com/a')).toBe('example.com/a')
  })

  it('ignores labelled links', () => {
    expect(shortenUrlText('my site', 'https://example.com')).toBeNull()
    expect(shortenUrlText('https://other.example', 'https://example.com')).toBeNull()
  })
})

describe('shortenBareLinks', () => {
  it('shortens bare links and adds a title, leaving mentions alone', () => {
    const url = 'https://github.com/kasuganosora/community/blob/master/frontend/src/main.ts'
    const html = `<p>see <a href="${url}">${url}</a> <a class="u-url mention" href="https://x.test/@a">@<span>a</span></a></p>`
    const out = shortenBareLinks(html)
    const div = document.createElement('div')
    div.innerHTML = out
    const [link, mention] = Array.from(div.querySelectorAll('a'))
    expect(link!.textContent).toBe('github.com/kasuganosora/commun…')
    expect(link!.getAttribute('href')).toBe(url)
    expect(link!.getAttribute('title')).toBe(url)
    expect(mention!.textContent).toBe('@a')
  })

  it('returns input unchanged when there are no links', () => {
    expect(shortenBareLinks('<p>hi</p>')).toBe('<p>hi</p>')
  })
})
