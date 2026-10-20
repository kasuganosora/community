import { describe, expect, it } from 'vitest'
import { chatEndpoint } from '../llmEndpoint'

describe('chatEndpoint', () => {
  it.each([
    ['https://api.example.com', '', 'https://api.example.com/v1/chat/completions'],
    ['https://api.example.com/v1/', '', 'https://api.example.com/v1/chat/completions'],
    ['https://open.bigmodel.cn/api/paas/v4', '', 'https://open.bigmodel.cn/api/paas/v4/chat/completions'],
    ['https://open.bigmodel.cn/api/coding/paas/v4', '', 'https://open.bigmodel.cn/api/coding/paas/v4/chat/completions'],
    ['https://x.example/v1beta', '', 'https://x.example/v1beta/chat/completions'],
    ['https://api.example.com/v1/chat/completions', '', 'https://api.example.com/v1/chat/completions'],
    ['https://api.example.com', 'custom/chat', 'https://api.example.com/custom/chat'],
    ['https://api.example.com', 'https://other.example/chat', 'https://other.example/chat'],
  ])('%s + %s', (base, path, want) => {
    expect(chatEndpoint(base, path)).toBe(want)
  })
})
