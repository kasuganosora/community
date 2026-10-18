import { describe, expect, it } from 'vitest'
import { diffKeywords, isValidDomain, normalizeDomain, parseLines } from '../lines'

describe('parseLines', () => {
  it('trims, skips blanks and de-duplicates', () => {
    expect(parseLines(' a \n\nb\r\na\n  \n')).toEqual(['a', 'b'])
  })
  it('de-duplicates after normalizing', () => {
    expect(parseLines('Example.com\nhttps://example.com/x', normalizeDomain)).toEqual(['example.com'])
  })
})

describe('domains', () => {
  it('normalizes URLs, ports, accts and trailing dots', () => {
    expect(normalizeDomain(' HTTPS://Foo.Example:8443/a?b ')).toBe('foo.example')
    expect(normalizeDomain('user@bar.example')).toBe('bar.example')
    expect(normalizeDomain('baz.example.')).toBe('baz.example')
  })
  it('validates like the server', () => {
    expect(isValidDomain('a.example')).toBe(true)
    expect(isValidDomain('例子.测试')).toBe(true)
    for (const bad of ['', 'localhost', 'a b.com', 'a..b', '-a.com', '*.a.com']) expect(isValidDomain(bad)).toBe(false)
  })
})

it('diffs keyword lists', () => {
  expect(diffKeywords(['a', 'b'], ['b', 'c'])).toEqual({ add: ['c'], remove: ['a'] })
})
