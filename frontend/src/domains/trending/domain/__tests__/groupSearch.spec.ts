import { describe, expect, it } from 'vitest'
import { groupClientLink, isGroupReference } from '../groupSearch'

describe('group search routing', () => {
  it.each(['#photography:matrix.org', '!old:matrix.org', '!opaque-v12', 'https://matrix.to/#/%23photography:matrix.org?via=example.test', 'https://matrix.to/#/!v12opaque?via=matrix.org', 'matrix:r/photography:matrix.org', 'matrix:roomid/opaque-v12?via=matrix.org'])('recognizes a room reference without resolving it: %s', value => {
    expect(isGroupReference(value)).toBe(true)
  })
  it.each(['摄影', '#photography', '@alice:matrix.org', 'matrix:u/alice:matrix.org', 'https://matrix.to/#/@alice:matrix.org', 'https://matrix.to.evil.test/#/#room:matrix.org', 'https://evil@matrix.to/#/#room:matrix.org', 'javascript:alert(1)'])('does not confuse other searches with room references: %s', value => {
    expect(isGroupReference(value)).toBe(false)
  })
  it('does not make protocol or credential-bearing URLs clickable', () => {
    expect(groupClientLink('javascript:alert(1)')).toBe('')
    expect(groupClientLink('https://user:secret@matrix.to/#/!room:example.test')).toBe('')
    expect(groupClientLink('https://matrix.to/#/!room:example.test')).toContain('https://matrix.to/')
  })
})
