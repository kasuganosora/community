import { describe, it, expect } from 'vitest'
import { validatePasswordChange, validateProfile, validateImageFile } from '../validation'

describe('validatePasswordChange', () => {
  it('requires the old password, length and matching confirmation', () => {
    expect(validatePasswordChange('', 'newpassword', 'newpassword')).toMatch('当前密码')
    expect(validatePasswordChange('old', 'short', 'short')).toMatch('至少')
    expect(validatePasswordChange('old', 'newpassword', 'different')).toMatch('不一致')
    expect(validatePasswordChange('samepassword', 'samepassword', 'samepassword')).toMatch('不能与')
    expect(validatePasswordChange('old', 'newpassword', 'newpassword')).toBeNull()
  })
})

describe('validateProfile', () => {
  it('counts characters, not UTF-16 units', () => {
    expect(validateProfile('😀'.repeat(30), '')).toBeNull()
    expect(validateProfile('名'.repeat(31), '')).toMatch('昵称')
    expect(validateProfile('ok', 'x'.repeat(501))).toMatch('简介')
  })
})

describe('validateImageFile', () => {
  it('accepts small images only', () => {
    expect(validateImageFile({ type: 'image/png', size: 1000 })).toBeNull()
    expect(validateImageFile({ type: 'text/plain', size: 10 })).toMatch('图片')
    expect(validateImageFile({ type: 'image/png', size: 9 * 1024 * 1024 })).toMatch('MB')
  })
})
