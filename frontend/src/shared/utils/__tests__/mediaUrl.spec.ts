import { describe, expect, it } from 'vitest'
import { sameOriginMediaUrl } from '../mediaUrl'

describe('sameOriginMediaUrl', () => {
  it('loads local uploads from the page origin instead of the configured site host', () => {
    expect(sameOriginMediaUrl('http://127.0.0.1:8080/media/4/photo.png')).toBe('/media/4/photo.png')
    expect(sameOriginMediaUrl('https://social.example.org/media/4/thumbnail/thumb.jpg?v=1')).toBe('/media/4/thumbnail/thumb.jpg?v=1')
  })
  it('leaves remote files alone', () => {
    expect(sameOriginMediaUrl('https://remote.test/full.png')).toBe('https://remote.test/full.png')
    expect(sameOriginMediaUrl('')).toBe('')
  })
})
