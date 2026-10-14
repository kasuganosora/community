import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import ImageCropDialog from '../ImageCropDialog.vue'

let wrappers: VueWrapper[] = []
let imageWidth = 1200
let imageHeight = 800
let failImage = false
const drawImage = vi.fn()
const toBlob = vi.fn<(callback: BlobCallback, type?: string) => void>()

beforeEach(() => {
  imageWidth = 1200; imageHeight = 800; failImage = false
  drawImage.mockReset()
  toBlob.mockReset().mockImplementation(callback => callback(new Blob(['cropped'], { type: 'image/png' })))
  vi.stubGlobal('Image', class {
    naturalWidth = imageWidth
    naturalHeight = imageHeight
    onload?: () => void
    onerror?: () => void
    set src(_value: string) { queueMicrotask(() => failImage ? this.onerror?.() : this.onload?.()) }
  })
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} })
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', { configurable: true, value: function (this: HTMLDialogElement) { this.setAttribute('open', '') } })
  Object.defineProperty(HTMLDialogElement.prototype, 'close', { configurable: true, value: function (this: HTMLDialogElement) { this.removeAttribute('open') } })
  vi.stubGlobal('URL', class extends URL {
    static createObjectURL = vi.fn(() => 'blob:crop-test')
    static revokeObjectURL = vi.fn()
  })
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({ drawImage } as unknown as CanvasRenderingContext2D)
  vi.spyOn(HTMLCanvasElement.prototype, 'toBlob').mockImplementation(toBlob)
})
afterEach(() => { wrappers.forEach(w => w.unmount()); wrappers = []; document.body.innerHTML = ''; vi.unstubAllGlobals(); vi.restoreAllMocks() })

function file(type = 'image/png') {
  const result = new File(['image'], 'source.png', { type })
  Object.defineProperty(result, 'arrayBuffer', { value: () => Promise.resolve(new ArrayBuffer(0)) })
  return result
}
async function setup(kind: 'avatar' | 'header' = 'avatar', source = file()) {
  const wrapper = mount(ImageCropDialog, { props: { file: source, kind }, attachTo: document.body, global: { stubs: { teleport: true } } })
  wrappers.push(wrapper)
  await flushPromises()
  return wrapper
}

it.each(['avatar', 'header'] as const)('exports the %s crop as transparent-capable PNG after applying', async kind => {
  const wrapper = await setup(kind)
  await wrapper.findAll('button').find(b => b.text() === '应用')!.trigger('click')
  await flushPromises()
  const result = wrapper.emitted('apply')![0]![0] as File
  expect(result.name).toBe('source-crop.png')
  expect(result.type).toBe('image/png')
  if (kind === 'avatar') expect(drawImage.mock.calls[0]!.slice(1)).toEqual([200, 0, 800, 800, 0, 0, 512, 512])
  else expect(drawImage.mock.calls[0]!.slice(1)).toEqual([0, 200, 1200, 400, 0, 0, 1200, 400])
})

it('cancels without exporting and returns focus while releasing the object URL', async () => {
  const trigger = document.createElement('button')
  document.body.append(trigger); trigger.focus()
  const wrapper = await setup()
  await wrapper.get('dialog').trigger('cancel')
  expect(wrapper.emitted('cancel')).toHaveLength(1)
  expect(wrapper.emitted('apply')).toBeUndefined()
  expect(drawImage).not.toHaveBeenCalled()
  wrapper.unmount(); wrappers = []
  expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:crop-test')
  expect(document.activeElement).toBe(trigger)
})

it('warns before flattening animation and allows selecting the unchanged original', async () => {
  const original = file('image/gif')
  const wrapper = await setup('avatar', original)
  expect(wrapper.get('[role=note]').text()).toContain('静态图片')
  await wrapper.findAll('button').find(b => b.text() === '使用原图')!.trigger('click')
  expect(wrapper.emitted('apply')![0]).toEqual([original])
  expect(drawImage).not.toHaveBeenCalled()
})

it('supports keyboard zoom and constrains movement to the image', async () => {
  const wrapper = await setup()
  await wrapper.get('.image-crop__viewport').trigger('keydown', { key: '+' })
  expect(wrapper.get('output').text()).toBe('110%')
  await wrapper.get('.image-crop__viewport').trigger('keydown', { key: 'ArrowRight', shiftKey: true })
  await wrapper.findAll('button').find(b => b.text() === '应用')!.trigger('click')
  await flushPromises()
  expect(drawImage.mock.calls[0]![1]).toBe(0)
})

it('does not emit a finished crop after the dialog was cancelled during encoding', async () => {
  let finish!: BlobCallback
  toBlob.mockImplementation(callback => { finish = callback })
  const wrapper = await setup()
  await wrapper.findAll('button').find(b => b.text() === '应用')!.trigger('click')
  wrapper.unmount(); wrappers = []
  finish(new Blob(['crop'], { type: 'image/png' }))
  await flushPromises()
  expect(wrapper.emitted('apply')).toBeUndefined()
})

it.each(['decode', 'oversized', 'unsupported'] as const)('handles %s failures without offering an invalid crop', async problem => {
  if (problem === 'decode') failImage = true
  if (problem === 'oversized') imageWidth = 50000
  const wrapper = await setup('avatar', file(problem === 'unsupported' ? 'image/svg+xml' : 'image/png'))
  expect(wrapper.get('[role=alert]').text()).toBeTruthy()
  expect(wrapper.findAll('button').find(b => b.text() === '应用')!.attributes('disabled')).toBeDefined()
  expect(wrapper.find('.image-crop__viewport').exists()).toBe(false)
  expect(wrapper.emitted('apply')).toBeUndefined()
})
