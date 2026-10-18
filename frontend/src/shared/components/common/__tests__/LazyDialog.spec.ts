import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h, nextTick, ref } from 'vue'
import LazyDialog from '../LazyDialog.vue'
import { Dialog } from 'tdesign-vue-next'

const { loadDialog } = vi.hoisted(() => ({ loadDialog: vi.fn() }))

// Load the CSS-free package entry during module setup. The mocked async
// boundary still proves that LazyDialog only requests Dialog on first open.
vi.mock('@/shared/ui/tdesignDialog', () => { loadDialog(); return { __esModule: true, default: Dialog } })

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
    expect(loadDialog).not.toHaveBeenCalled()
    visible.value = true
    await settle()
    expect(document.querySelector('.t-dialog')).not.toBeNull()
    expect(loadDialog).toHaveBeenCalledTimes(1)
    expect(document.querySelector('.dialog-body')?.textContent).toBe('内容')
    w.unmount()
  })
})
