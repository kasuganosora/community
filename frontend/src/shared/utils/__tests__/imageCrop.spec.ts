import { describe, expect, it } from 'vitest'
import { clampCropCenter, imageCropRect, imageMayAnimate } from '../imageCrop'

describe('profile image crop geometry', () => {
  it('centers a square in a landscape avatar and a 3:1 cover in a portrait image', () => {
    expect(imageCropRect({ width: 1200, height: 800 }, 1, 1, { x: .5, y: .5 })).toEqual({ x: 200, y: 0, width: 800, height: 800 })
    expect(imageCropRect({ width: 900, height: 1800 }, 3, 1, { x: .5, y: .5 })).toEqual({ x: 0, y: 750, width: 900, height: 300 })
  })
  it('never exposes blank space while dragging or zooming out at an edge', () => {
    const image = { width: 1200, height: 800 }
    const center = clampCropCenter(image, 3, 1, { x: -5, y: 8 })
    expect(center).toEqual({ x: .5, y: .75 })
    expect(imageCropRect(image, 3, 4, { x: 50, y: -50 })).toEqual({ x: 900, y: 0, width: 300, height: 100 })
    expect(imageCropRect(image, 3, -1, center)).toEqual({ x: 0, y: 400, width: 1200, height: 400 })
  })
  it('preserves bounds and aspect for small, extreme, and fractional source sizes', () => {
    for (const width of [1, 17, 500, 16384]) for (const height of [1, 31, 800, 16384]) {
      for (const aspect of [1, 3]) for (const zoom of [1, 1.37, 4, 50]) {
        const crop = imageCropRect({ width, height }, aspect, zoom, { x: 5, y: -.5 })
        expect(crop.x).toBeGreaterThanOrEqual(0)
        expect(crop.y).toBeGreaterThanOrEqual(0)
        expect(crop.x + crop.width).toBeLessThanOrEqual(width + 1e-6)
        expect(crop.y + crop.height).toBeLessThanOrEqual(height + 1e-6)
        expect(crop.width / crop.height).toBeCloseTo(aspect)
      }
    }
  })
  it('rejects zero or nonfinite image coordinates', () => {
    expect(() => imageCropRect({ width: 0, height: 50 }, 1, 1, { x: .5, y: .5 })).toThrow()
    expect(() => imageCropRect({ width: 50, height: 50 }, 1, 1, { x: NaN, y: .5 })).toThrow()
  })
})

it('detects GIF, animated WebP and APNG while keeping static formats quiet', () => {
  expect(imageMayAnimate('image/gif', new Uint8Array())).toBe(true)
  const webp = new Uint8Array(21)
  webp.set([86, 80, 56, 88], 12); webp[20] = 2
  expect(imageMayAnimate('image/webp', webp)).toBe(true)
  webp[20] = 0
  expect(imageMayAnimate('image/webp', webp)).toBe(false)
  const png = new Uint8Array(20)
  png.set([97, 99, 84, 76], 12)
  expect(imageMayAnimate('image/png', png)).toBe(true)
  png.set([73, 68, 65, 84], 12)
  expect(imageMayAnimate('image/png', png)).toBe(false)
  expect(imageMayAnimate('image/png', new Uint8Array(7))).toBe(false)
  expect(imageMayAnimate('image/jpeg', new Uint8Array())).toBe(false)
})
