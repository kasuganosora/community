import { describe, expect, it } from 'vitest'
import { DEFAULT_TIMEOUT_MS, SLOW_NETWORK_TIMEOUT_MS, isTimeoutError, requestTimeout } from '../client'

describe('weak-network request handling', () => {
  it('gives slow connections a longer timeout', () => {
    expect(requestTimeout(undefined)).toBe(DEFAULT_TIMEOUT_MS)
    expect(requestTimeout({ effectiveType: '4g', rtt: 50, downlink: 10 })).toBe(DEFAULT_TIMEOUT_MS)
    expect(requestTimeout({ effectiveType: '3g' })).toBe(SLOW_NETWORK_TIMEOUT_MS)
    expect(requestTimeout({ effectiveType: '4g', saveData: true })).toBe(SLOW_NETWORK_TIMEOUT_MS)
    expect(requestTimeout({ effectiveType: '4g', rtt: 600 })).toBe(SLOW_NETWORK_TIMEOUT_MS)
  })
  it('recognises axios timeouts', () => {
    expect(isTimeoutError({ code: 'ECONNABORTED', message: 'timeout of 15000ms exceeded' })).toBe(true)
    expect(isTimeoutError({ code: 'ETIMEDOUT' })).toBe(true)
    expect(isTimeoutError({ code: 'ERR_NETWORK', message: 'Network Error' })).toBe(false)
  })
})
