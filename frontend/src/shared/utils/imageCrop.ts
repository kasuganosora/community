export interface ImageSize { width: number; height: number }
export interface CropCenter { x: number; y: number }
export interface CropRect extends ImageSize { x: number; y: number }

export const MAX_CROP_ZOOM = 4
export const PROFILE_HEADER_RATIO = 3

/** All coordinates are in the browser-decoded, EXIF-oriented image. */
export function imageCropRect(image: ImageSize, aspect: number, zoom: number, center: CropCenter): CropRect {
  if (![image.width, image.height, aspect, zoom, center.x, center.y].every(Number.isFinite)
    || image.width <= 0 || image.height <= 0 || aspect <= 0) throw new Error('无效的图片尺寸')
  const scale = Math.min(MAX_CROP_ZOOM, Math.max(1, zoom))
  const width = Math.min(image.width, image.height * aspect) / scale
  const height = width / aspect
  return {
    width, height,
    x: Math.max(0, Math.min(image.width - width, center.x * image.width - width / 2)),
    y: Math.max(0, Math.min(image.height - height, center.y * image.height - height / 2)),
  }
}

export function clampCropCenter(image: ImageSize, aspect: number, zoom: number, center: CropCenter): CropCenter {
  const crop = imageCropRect(image, aspect, zoom, center)
  return { x: (crop.x + crop.width / 2) / image.width, y: (crop.y + crop.height / 2) / image.height }
}

/** GIF may be animated; PNG/WebP/AVIF advertise animation in their headers. */
export function imageMayAnimate(type: string, bytes: Uint8Array): boolean {
  if (type === 'image/gif') return true
  const text = (offset: number, length: number) => String.fromCharCode(...bytes.subarray(offset, offset + length))
  if (type === 'image/webp') return text(12, 4) === 'VP8X' && !!((bytes[20] ?? 0) & 2)
  if (type === 'image/avif') return text(0, 64).includes('avis')
  if (type === 'image/png') {
    const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength)
    for (let offset = 8; offset + 12 <= bytes.length;) {
      if (text(offset + 4, 4) === 'acTL') return true
      // acTL precedes the first IDAT in an APNG.
      if (text(offset + 4, 4) === 'IDAT') return false
      offset += 12 + view.getUint32(offset)
    }
  }
  return false
}
