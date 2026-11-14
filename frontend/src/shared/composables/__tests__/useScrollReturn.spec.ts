import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, ref, type PropType } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { mount } from '@vue/test-utils'
import { invalidateSessionData } from '@/shared/api/sessionScope'
import TabBar from '@/shared/components/common/TabBar.vue'
import {
  clearScrollReturnMemory,
  hasSavedScrollReturn,
  triggerScrollTopOrReturn,
  useScrollReturn,
  type ScrollAnchorPoint,
  type ScrollReturnAdapter,
} from '../useScrollReturn'

const ScrollReturnHarness = defineComponent({
  components: { TabBar },
  props: {
    scope: { type: String, required: true },
    adapter: { type: Object as PropType<ScrollReturnAdapter>, default: undefined },
    withTabs: { type: Boolean, default: false },
  },
  setup(props) {
    const tab = ref('following')
    const { onHeaderTap } = useScrollReturn(
      () => (props.withTabs ? `home:${tab.value}` : props.scope),
      props.adapter,
    )
    return {
      tab,
      onHeaderTap,
      options: [
        { value: 'following', label: '关注' },
        { value: 'global', label: '全站' },
      ],
    }
  },
  template: `
    <div>
      <div class="page-header" @click="onHeaderTap">
        <button class="back-btn" type="button">返回</button>
        <h1 class="page-header__title">首页</h1>
      </div>
      <TabBar v-if="withTabs" v-model="tab" :options="options" label="时间线" />
    </div>
  `,
})

describe('Tweetbot Tap-to-Top and Return-to-Last-Read', () => {
  let scrollY = 0
  let clock = 1000

  beforeEach(() => {
    scrollY = 0
    clock = 1000
    clearScrollReturnMemory()
    vi.spyOn(performance, 'now').mockImplementation(() => clock)
    window.matchMedia = vi.fn().mockReturnValue({ matches: true }) as unknown as typeof window.matchMedia
    vi.spyOn(window, 'scrollY', 'get').mockImplementation(() => scrollY)
    vi.spyOn(window, 'scrollTo').mockImplementation((arg: ScrollToOptions | number, y?: number) => {
      scrollY = typeof arg === 'object' ? (arg.top ?? scrollY) : (y ?? scrollY)
    })
  })

  afterEach(() => {
    clearScrollReturnMemory()
    vi.restoreAllMocks()
  })

  it('scrolls to top on header tap while saving anchor, returns on second tap, and ignores interactive child buttons', async () => {
    const restored: ScrollAnchorPoint[] = []
    const wrapper = mount(ScrollReturnHarness, {
      props: {
        scope: 'home:following',
        adapter: {
          capture: () => ({ id: 'post-42', top: -28, scrollY }),
          restore: (point) => {
            restored.push(point)
            scrollY = point.scrollY
          },
          scrollToTop: () => { scrollY = 0 },
        },
      },
    })
    scrollY = 1280

    // Clicking an interactive button inside the header must not trigger scroll-to-top.
    await wrapper.get('.back-btn').trigger('click')
    expect(scrollY).toBe(1280)
    expect(hasSavedScrollReturn('home:following')).toBe(false)

    // Tapping the header title saves the reading anchor and scrolls to 0.
    await wrapper.get('.page-header__title').trigger('click')
    expect(scrollY).toBe(0)
    expect(hasSavedScrollReturn('home:following')).toBe(true)

    // Tapping again while at top returns directly to the saved post anchor!
    await wrapper.get('.page-header__title').trigger('click')
    expect(restored).toEqual([{ id: 'post-42', top: -28, scrollY: 1280 }])
    expect(scrollY).toBe(1280)
    expect(hasSavedScrollReturn('home:following')).toBe(false)

    wrapper.unmount()
  })

  it('expires the saved return bookmark if the user manually scrolls back down into the feed', () => {
    const wrapper = mount(ScrollReturnHarness, { props: { scope: 'explore:local' } })

    scrollY = 960
    triggerScrollTopOrReturn()
    expect(scrollY).toBe(0)
    expect(hasSavedScrollReturn('explore:local')).toBe(true)

    // User manually scrolls past 200px after programmatic window expires.
    clock += 1500
    scrollY = 320
    window.dispatchEvent(new Event('scroll'))
    expect(hasSavedScrollReturn('explore:local')).toBe(false)

    // Next trigger goes up to 0 instead of jumping to 960.
    triggerScrollTopOrReturn()
    expect(scrollY).toBe(0)

    wrapper.unmount()
  })

  it('triggers scroll-to-top / return when re-tapping the already active TabBar tab, and clears on session change', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/', component: ScrollReturnHarness }],
    })
    await router.push('/')

    const wrapper = mount(ScrollReturnHarness, {
      props: { scope: 'home:following', withTabs: true },
      global: { plugins: [router] },
    })
    scrollY = 840

    const activeTabBtn = wrapper.findAll('[role="tab"]')[0]!
    await activeTabBtn.trigger('click')
    expect(scrollY).toBe(0)
    expect(hasSavedScrollReturn('home:following')).toBe(true)

    // Re-tapping again returns to 840.
    await activeTabBtn.trigger('click')
    expect(scrollY).toBe(840)

    // Save again, then invalidate session -> saved position is wiped.
    await activeTabBtn.trigger('click')
    expect(hasSavedScrollReturn('home:following')).toBe(true)
    invalidateSessionData()
    expect(hasSavedScrollReturn('home:following')).toBe(false)

    wrapper.unmount()
  })
})
