import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h, nextTick, ref } from 'vue'
import LazyDialog from '../LazyDialog.vue'

// The real Dialog without its stylesheet (Node cannot import .css).
vi.mock('@/shared/ui/tdesignDialog', async () => ({ __esModule: true, default: (await import('tdesign-vue-next')).Dialog }))

async function settle() {
  for (let i = 0; i < 5; i++) { await flushPromises(); await new Promise(r => setTimeout(r, 20)); await nextTick() }
}

describe('LazyDialog', () => {
  it('loads TDesign Dialog only when first opened and passes slots and v-model through', async () => {
    const visible = ref(false)
    const Host = defineComponent(() => () => h(LazyDialog, { 'visible': visible.value, 'onUpdate:visible': (v: boolean) => { visible.value = v }, 'header': '标题', 'footer': false, 'attach': 'body' }, { default: () => h('p', { class: 'dialog-body' }, '内容') }))
    const w = mount(Host, { attachTo: document.body })
    await settle()
    expect(document.querySelector('.t-dialog')).toBeNull()
    visible.value = true
    await settle()
    expect(document.querySelector('.t-dialog')).not.toBeNull()
    expect(document.querySelector('.dialog-body')?.textContent).toBe('内容')
    w.unmount()
  })
})
