import { describe, expect, it } from 'vitest'
import { dedupeMirroredStatuses, embeddedLocalID, sharesStatusIdentity } from './statusIdentity'

const origin = window.location.origin
const local = {
  id: '15',
  uri: `${origin}/users/kanna/statuses/15`,
  url: `${origin}/@kanna/15`,
  account: { acct: 'kanna' },
  content: 'same',
}
const mirror = {
  id: '23',
  uri: `${origin}/users/kanna/statuses/15`,
  url: `${origin}/users/kanna/statuses/15`,
  account: { acct: `kanna@${window.location.hostname}` },
  content: 'same',
}

describe('mirrored status identity', () => {
  it('reads a local permalink id only from this instance', () => {
    expect(embeddedLocalID(`${origin}/users/kanna/statuses/15`)).toBe('15')
    expect(embeddedLocalID(`${origin}/@kanna/status/15`)).toBe('15')
    expect(embeddedLocalID(`${origin}/@kanna/15`)).toBe('15')
    expect(embeddedLocalID('https://other.example/users/kanna/statuses/15')).toBeNull()
  })

  it('keeps the local row and drops the remote mirror, including a same-person reblog', () => {
    const boost = { id: '27', uri: 'https://other.example/users/kanna/statuses/90', account: { acct: `kanna@${window.location.hostname}` }, reblog: { id: '15' } }
    const stranger = { id: '28', uri: 'https://other.example/users/bob/statuses/9', account: { acct: 'bob@other.example' }, reblog: { id: '15' } }
    expect(dedupeMirroredStatuses([mirror, local, boost, stranger]).map(row => row.id)).toEqual(['15', '28'])
  })

  it('treats a streamed mirror id as the local row already on screen', () => {
    expect(sharesStatusIdentity(local, { id: '23', uri: local.uri })).toBe(true)
    expect(sharesStatusIdentity(local, { id: '23', acct: mirror.account.acct, reblogId: '15' })).toBe(true)
    expect(sharesStatusIdentity(local, { id: '28', acct: 'bob@other.example', reblogId: '15' })).toBe(false)
  })
})
