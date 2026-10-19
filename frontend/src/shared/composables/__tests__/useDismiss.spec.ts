import { mount } from '@vue/test-utils'
import { defineComponent, h, ref } from 'vue'
import { describe, expect, it } from 'vitest'
import { useDismiss } from '../useDismiss'

function harness(options: Parameters<typeof useDismiss>[2] = {}) {
  const open = ref(true)
  const Comp = defineComponent({
    setup() {
      const root = ref<HTMLElement>()
      useDismiss(open, () => root.value, options)
      return () => h('div', [h('div', { ref: root, class: 'menu' }, [h('button', { class: 'inside' })]), h('button', { class: 'outside' })])
    },
  })
  return { open, wrapper: mount(Comp, { attachTo: document.body }) }
}

describe('useDismiss', () => {
  it('ignores pointerdown inside and closes on pointerdown outside', () => {
    const { open, wrapper } = harness()
    wrapper.get('.inside').element.dispatchEvent(new Event('pointerdown', { bubbles: true }))
    expect(open.value).toBe(true)
    wrapper.get('.outside').element.dispatchEvent(new Event('pointerdown', { bubbles: true }))
    expect(open.value).toBe(false)
    wrapper.unmount()
  })
  it('closes on Escape, focus leaving and page scroll', async () => {
    const { open, wrapper } = harness()
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    expect(open.value).toBe(false)
    open.value = true; await wrapper.vm.$nextTick()
    ;(wrapper.get('.inside').element as HTMLElement).focus()
    expect(open.value).toBe(true)
    ;(wrapper.get('.outside').element as HTMLElement).focus()
    expect(open.value).toBe(false)
    open.value = true; await wrapper.vm.$nextTick()
    wrapper.get('.menu').element.dispatchEvent(new Event('scroll'))
    expect(open.value).toBe(true)
    document.dispatchEvent(new Event('scroll'))
    expect(open.value).toBe(false)
    wrapper.unmount()
  })
  it('reports the reason and can opt out of scroll', () => {
    const reasons: string[] = []
    const { open, wrapper } = harness({ scroll: false, onClose: r => { reasons.push(r); open.value = false } })
    document.dispatchEvent(new Event('scroll'))
    expect(open.value).toBe(true)
    document.body.dispatchEvent(new Event('pointerdown', { bubbles: true }))
    expect(reasons).toEqual(['outside'])
    wrapper.unmount()
  })
})
