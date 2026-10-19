import { mount } from '@vue/test-utils'
import { defineComponent, h, ref } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import { useMediaDrop, transferFiles, acceptImages } from '../useMediaDrop'

function payload(files: File[], text = '') {
  return {
    items: files.map(file => ({ kind: 'file', getAsFile: () => file })),
    files,
    types: [...(files.length ? ['Files'] : []), ...(text ? ['text/plain'] : [])],
    getData: (type: string) => type === 'text/plain' ? text : '',
    dropEffect: 'none',
  } as unknown as DataTransfer
}
const png = (name: string) => new File(['x'], name, { type: 'image/png' })

function harness(enabled = () => true) {
  const onFiles = vi.fn()
  const Comp = defineComponent({
    setup() {
      const el = ref<HTMLElement>()
      const { dragging } = useMediaDrop(el, { onFiles, enabled })
      return () => h('div', { ref: el, 'data-dragging': String(dragging.value) }, [h('textarea')])
    },
  })
  return { wrapper: mount(Comp, { attachTo: document.body }), onFiles }
}
function fire(target: Element, type: string, data: DataTransfer) {
  const event = new Event(type, { bubbles: true, cancelable: true })
  Object.defineProperty(event, type === 'paste' ? 'clipboardData' : 'dataTransfer', { value: data })
  target.dispatchEvent(event)
  return event
}

describe('useMediaDrop', () => {
  it('uploads every pasted image and cancels an image-only paste', () => {
    const { wrapper, onFiles } = harness()
    const event = fire(wrapper.get('textarea').element, 'paste', payload([png('a.png'), png('b.png')]))
    expect(onFiles).toHaveBeenCalledWith([expect.objectContaining({ name: 'a.png' }), expect.objectContaining({ name: 'b.png' })])
    expect(event.defaultPrevented).toBe(true)
    wrapper.unmount()
  })
  it('keeps the text of a mixed paste and leaves plain text alone', () => {
    const { wrapper, onFiles } = harness()
    const mixed = fire(wrapper.get('textarea').element, 'paste', payload([png('a.png')], 'hello'))
    expect(mixed.defaultPrevented).toBe(false)
    expect(onFiles).toHaveBeenCalledTimes(1)
    const text = fire(wrapper.get('textarea').element, 'paste', payload([], 'just text'))
    expect(text.defaultPrevented).toBe(false)
    expect(onFiles).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
  it('attaches dropped files, highlights while dragging, and respects enabled()', async () => {
    let on = true
    const { wrapper, onFiles } = harness(() => on)
    fire(wrapper.element, 'dragenter', payload([png('a.png')]))
    await wrapper.vm.$nextTick()
    expect(wrapper.attributes('data-dragging')).toBe('true')
    const drop = fire(wrapper.element, 'drop', payload([png('a.png'), new File(['x'], 'doc.txt', { type: 'text/plain' })]))
    expect(drop.defaultPrevented).toBe(true)
    expect(onFiles).toHaveBeenCalledWith([expect.objectContaining({ name: 'a.png' })])
    await wrapper.vm.$nextTick()
    expect(wrapper.attributes('data-dragging')).toBe('false')
    on = false
    fire(wrapper.get('textarea').element, 'paste', payload([png('c.png')]))
    expect(onFiles).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
  it('reads files from items or the files list', () => {
    expect(transferFiles(payload([png('a.png')]), acceptImages)).toHaveLength(1)
    expect(transferFiles({ items: [], files: [png('b.png')] } as unknown as DataTransfer)).toHaveLength(1)
    expect(transferFiles(null)).toEqual([])
  })
})
