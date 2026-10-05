import client from '@/shared/api/client'

export interface CaptchaConfig {
  enabled: boolean
  provider: string
  site_key: string
}

export type LocalFrame =
  | { kind: 'image'; url: string; index: number; total: number; waitMs: number; done: boolean }
  | { kind: 'wait'; waitMs: number; done: boolean; index: number; total: number }

export const captchaApi = {
  async getConfig(): Promise<CaptchaConfig> {
    const { data } = await client.get<Partial<CaptchaConfig>>('/api/v1/captcha/config')
    return {
      enabled: data.enabled === true,
      provider: data.provider || 'none',
      site_key: data.site_key || '',
    }
  },

  async startLocal(): Promise<{ id: string; total: number; prompt: string; pow_salt: string; pow_bits: number }> {
    const { data } = await client.post<{ id: string; total: number; prompt: string; pow_salt: string; pow_bits: number }>('/api/v1/captcha/local/start', {})
    return data
  },

  async fetchLocalFrame(id: string): Promise<LocalFrame> {
    const res = await client.get<ArrayBuffer>(`/api/v1/captcha/local/${encodeURIComponent(id)}/frame`, {
      responseType: 'arraybuffer',
      headers: { Accept: 'image/gif, application/json' },
    })
    const type = String(res.headers['content-type'] || '')
    if (type.includes('image')) {
      const blob = new Blob([res.data], { type: 'image/gif' })
      return {
        kind: 'image',
        url: URL.createObjectURL(blob),
        index: Number(res.headers['x-captcha-index'] || 0),
        total: Number(res.headers['x-captcha-total'] || 5),
        waitMs: Number(res.headers['x-captcha-wait-ms'] || 0),
        done: res.headers['x-captcha-done'] === '1',
      }
    }
    const body = JSON.parse(new TextDecoder().decode(res.data)) as {
      wait_ms?: number
      done?: boolean
      index?: number
      total?: number
    }
    return {
      kind: 'wait',
      waitMs: body.wait_ms || 0,
      done: body.done === true,
      index: body.index || 0,
      total: body.total || 5,
    }
  },

  async verifyLocal(id: string, answer: string, nonce: string, homepage = ''): Promise<{ token: string }> {
    const { data } = await client.post<{ token: string }>(`/api/v1/captcha/local/${encodeURIComponent(id)}/verify`, {
      answer,
      nonce,
      homepage,
    })
    return data
  },
}

export async function solveCaptchaPow(salt: string, bits: number): Promise<string> {
  const enc = new TextEncoder()
  for (let i = 0; i < 1 << 20; i++) {
    const nonce = i.toString(16)
    const digest = sha256(enc.encode(`${salt}:${nonce}`))
    if (leadingZeroBits(digest) >= bits) return nonce
    if ((i & 255) === 255) await new Promise((resolve) => setTimeout(resolve, 0))
  }
  throw new Error('验证码确认失败，请换一个')
}

function rotr(x: number, n: number) {
  return (x >>> n) | (x << (32 - n))
}

// Pure SHA-256. crypto.subtle is missing on plain HTTP, and this site can be served that way.
function sha256(message: Uint8Array): Uint8Array {
  const k = [
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
    0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
    0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
    0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
    0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
    0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
  ]
  const padded = new Uint8Array((((message.length + 9 + 63) >> 6) << 6))
  padded.set(message)
  padded[message.length] = 0x80
  const view = new DataView(padded.buffer)
  const bits = message.length * 8
  view.setUint32(padded.length - 8, Math.floor(bits / 0x100000000))
  view.setUint32(padded.length - 4, bits >>> 0)

  let h0 = 0x6a09e667
  let h1 = 0xbb67ae85
  let h2 = 0x3c6ef372
  let h3 = 0xa54ff53a
  let h4 = 0x510e527f
  let h5 = 0x9b05688c
  let h6 = 0x1f83d9ab
  let h7 = 0x5be0cd19
  const w = new Uint32Array(64)

  for (let i = 0; i < padded.length; i += 64) {
    for (let t = 0; t < 16; t++) w[t] = view.getUint32(i + t * 4)
    for (let t = 16; t < 64; t++) {
      const s0 = rotr(w[t - 15], 7) ^ rotr(w[t - 15], 18) ^ (w[t - 15] >>> 3)
      const s1 = rotr(w[t - 2], 17) ^ rotr(w[t - 2], 19) ^ (w[t - 2] >>> 10)
      w[t] = (w[t - 16] + s0 + w[t - 7] + s1) >>> 0
    }
    let a = h0
    let b = h1
    let c = h2
    let d = h3
    let e = h4
    let f = h5
    let g = h6
    let h = h7
    for (let t = 0; t < 64; t++) {
      const s1 = rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25)
      const ch = (e & f) ^ (~e & g)
      const temp1 = (h + s1 + ch + k[t] + w[t]) >>> 0
      const s0 = rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22)
      const maj = (a & b) ^ (a & c) ^ (b & c)
      const temp2 = (s0 + maj) >>> 0
      h = g
      g = f
      f = e
      e = (d + temp1) >>> 0
      d = c
      c = b
      b = a
      a = (temp1 + temp2) >>> 0
    }
    h0 = (h0 + a) >>> 0
    h1 = (h1 + b) >>> 0
    h2 = (h2 + c) >>> 0
    h3 = (h3 + d) >>> 0
    h4 = (h4 + e) >>> 0
    h5 = (h5 + f) >>> 0
    h6 = (h6 + g) >>> 0
    h7 = (h7 + h) >>> 0
  }

  const out = new Uint8Array(32)
  const ov = new DataView(out.buffer)
  ;[h0, h1, h2, h3, h4, h5, h6, h7].forEach((v, i) => ov.setUint32(i * 4, v))
  return out
}

function leadingZeroBits(bytes: Uint8Array): number {
  let n = 0
  for (const b of bytes) {
    if (b === 0) {
      n += 8
      continue
    }
    for (let mask = 0x80; mask > 0; mask >>= 1) {
      if (b & mask) return n
      n++
    }
  }
  return n
}

const loading = new Map<string, Promise<void>>()

/** Load a captcha provider script once. A failed load can be retried. */
export function loadCaptchaScript(src: string): Promise<void> {
  const pending = loading.get(src)
  if (pending) return pending

  const task = new Promise<void>((resolve, reject) => {
    const prior = document.querySelector<HTMLScriptElement>(`script[data-captcha-src="${src}"]`)
    if (prior?.dataset.loaded === '1') {
      resolve()
      return
    }
    const script = prior ?? document.createElement('script')
    script.dataset.captchaSrc = src
    script.async = true
    script.src = src
    script.onload = () => {
      script.dataset.loaded = '1'
      resolve()
    }
    script.onerror = () => {
      loading.delete(src)
      script.remove()
      reject(new Error('验证码脚本加载失败'))
    }
    if (!prior) document.head.appendChild(script)
  })
  loading.set(src, task)
  return task
}
