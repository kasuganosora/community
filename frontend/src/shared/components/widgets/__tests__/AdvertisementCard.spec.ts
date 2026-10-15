import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

const { advertisements } = vi.hoisted(() => ({ advertisements: vi.fn() }))
vi.mock('@/domains/admin/infrastructure/communityAssetsApi', () => ({ communityAssetsApi: { advertisements } }))
import AdvertisementCard from '../AdvertisementCard.vue'

beforeEach(() => { vi.useFakeTimers(); localStorage.clear(); advertisements.mockReset() })
afterEach(() => vi.useRealTimers())

it('defers the first request until idle and caches an empty answer', async () => {
  advertisements.mockResolvedValue([])
  const w = mount(AdvertisementCard)
  expect(advertisements).not.toHaveBeenCalled()
  await vi.advanceTimersByTimeAsync(4100)
  await flushPromises()
  expect(advertisements).toHaveBeenCalledTimes(1)
  expect(JSON.parse(localStorage.getItem('community:ads:v1')!).rows).toEqual([])
  w.unmount()
  const w2 = mount(AdvertisementCard)
  await vi.advanceTimersByTimeAsync(4100)
  expect(advertisements).toHaveBeenCalledTimes(1)
  w2.unmount()
})
