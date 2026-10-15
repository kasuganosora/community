import { readdir, readFile, stat, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { brotliCompressSync, constants, gzipSync } from 'node:zlib'
import type { Plugin, ResolvedConfig } from 'vite'

// Text assets worth compressing; images/fonts are already compressed.
const COMPRESSIBLE = /\.(js|mjs|css|html|svg|json|webmanifest|txt|xml)$/
const MIN_BYTES = 1024

async function* walk(dir: string): AsyncGenerator<string> {
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name)
    if (entry.isDirectory()) yield* walk(full)
    else yield full
  }
}

/**
 * Writes <file>.br and <file>.gz next to every compressible build output so
 * the Go backend can serve them as-is (no per-request compression cost).
 * Variants that do not shrink the file are skipped.
 */
export function precompress(): Plugin {
  let config: ResolvedConfig
  return {
    name: 'community:precompress',
    apply: 'build',
    configResolved(c) { config = c },
    async closeBundle() {
      const outDir = path.resolve(config.root, config.build.outDir)
      for await (const file of walk(outDir)) {
        if (!COMPRESSIBLE.test(file)) continue
        if ((await stat(file)).size < MIN_BYTES) continue
        const data = await readFile(file)
        const br = brotliCompressSync(data, { params: { [constants.BROTLI_PARAM_QUALITY]: 11, [constants.BROTLI_PARAM_SIZE_HINT]: data.length } })
        if (br.length < data.length) await writeFile(file + '.br', br)
        const gz = gzipSync(data, { level: 9 })
        if (gz.length < data.length) await writeFile(file + '.gz', gz)
      }
    },
  }
}
