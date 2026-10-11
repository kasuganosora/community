import { describe, expect, it } from 'vitest'
import { displayAccount, displayDomain } from '../accountDisplay'

describe('account domain presentation', () => {
  it('decodes IDN labels while preserving the account username and canonical input', () => {
    const canonical = 'chn@xn--s8w913fdga.chn.moe'
    const rendered = displayAccount(canonical)
    expect(rendered).toBe('chn@铜锣湾.chn.moe')
    expect(rendered.startsWith('chn@')).toBe(true)
    expect(new URL(`https://${rendered.split('@')[1]}`).hostname).toBe(canonical.split('@')[1])
    expect(canonical).toBe('chn@xn--s8w913fdga.chn.moe')
    expect(displayAccount('alice@xn--bcher-kva.example')).toBe('alice@bücher.example')
  })
  it('supports full handles, Matrix IDs, multiple labels and ports', () => {
    expect(displayAccount('@alice@xn--bcher-kva.example:8448')).toBe('@alice@bücher.example:8448')
    expect(displayAccount('@alice:xn--bcher-kva.example')).toBe('@alice:bücher.example')
    expect(displayDomain('xn--fsqu00a.xn--0zwm56d')).toBe('例子.测试')
  })
  it('keeps local usernames, ASCII, existing Unicode and invalid labels intact', () => {
    for (const account of ['alice', 'xn--bcher-kva', '@alice', 'alice@example.org', 'alice@例子.测试', 'alice@xn--.example', 'alice@xn--bcher-kva.example/path']) {
      expect(displayAccount(account)).toBe(account)
    }
    expect(displayAccount(undefined)).toBe('')
  })
})
