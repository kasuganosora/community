import { describe, it, expect } from 'vitest'
import { validateListTitle } from '../list'

describe('validateListTitle', () => {
  it('rejects empty and overly long titles', () => {
    expect(validateListTitle('   ')).toMatch('请输入')
    expect(validateListTitle('列'.repeat(65))).toMatch('64')
    expect(validateListTitle('朋友')).toBeNull()
  })
})
