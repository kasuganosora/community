/** Minimal BlurHash decoder (https://blurha.sh), enough for placeholders. */
const ALPHABET = '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz#$%*+,-.:;=?@[]^_{|}~'

function decode83(str: string): number {
  let value = 0
  for (const ch of str) {
    const digit = ALPHABET.indexOf(ch)
    if (digit < 0) throw new Error('invalid blurhash character')
    value = value * 83 + digit
  }
  return value
}

const srgbToLinear = (v: number) => {
  const x = v / 255
  return x <= 0.04045 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4
}
const linearToSrgb = (v: number) => {
  const x = Math.max(0, Math.min(1, v))
  return Math.trunc(x <= 0.0031308 ? x * 12.92 * 255 + 0.5 : (1.055 * x ** (1 / 2.4) - 0.055) * 255 + 0.5)
}
const signPow = (v: number, exp: number) => Math.sign(v) * Math.abs(v) ** exp

/** True when hash has a valid length for its own component counts. */
export function isValidBlurhash(hash: string | null | undefined): hash is string {
  if (!hash || hash.length < 6) return false
  try {
    const size = decode83(hash[0])
    const numY = Math.floor(size / 9) + 1
    const numX = (size % 9) + 1
    return hash.length === 4 + 2 * numX * numY
  } catch {
    return false
  }
}

/** Decode to RGBA pixels (width * height * 4). Throws on an invalid hash. */
export function decodeBlurhash(hash: string, width: number, height: number, punch = 1): Uint8ClampedArray {
  if (!isValidBlurhash(hash)) throw new Error('invalid blurhash')
  const size = decode83(hash[0])
  const numY = Math.floor(size / 9) + 1
  const numX = (size % 9) + 1
  const maxValue = (decode83(hash[1]) + 1) / 166
  const colors: number[][] = []
  for (let i = 0; i < numX * numY; i++) {
    if (i === 0) {
      const v = decode83(hash.substring(2, 6))
      colors.push([srgbToLinear(v >> 16), srgbToLinear((v >> 8) & 255), srgbToLinear(v & 255)])
    } else {
      const v = decode83(hash.substring(4 + i * 2, 6 + i * 2))
      const q = [Math.floor(v / 361), Math.floor(v / 19) % 19, v % 19]
      colors.push(q.map(c => signPow((c - 9) / 9, 2) * maxValue * punch))
    }
  }
  const pixels = new Uint8ClampedArray(width * height * 4)
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      let r = 0, g = 0, b = 0
      for (let j = 0; j < numY; j++) {
        for (let i = 0; i < numX; i++) {
          const basis = Math.cos((Math.PI * x * i) / width) * Math.cos((Math.PI * y * j) / height)
          const c = colors[i + j * numX]
          r += c[0] * basis
          g += c[1] * basis
          b += c[2] * basis
        }
      }
      const o = 4 * (x + y * width)
      pixels[o] = linearToSrgb(r)
      pixels[o + 1] = linearToSrgb(g)
      pixels[o + 2] = linearToSrgb(b)
      pixels[o + 3] = 255
    }
  }
  return pixels
}
