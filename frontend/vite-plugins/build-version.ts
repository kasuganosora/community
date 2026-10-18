import { readFile } from 'node:fs/promises'
import path from 'node:path'
import type { Plugin } from 'vite'

/**
 * Build id shared by the client bundle (__BUILD_ID__), /version.json and the
 * service worker. Override with COMMUNITY_BUILD_ID (e.g. the git sha) for
 * reproducible ids; otherwise every build gets a fresh one.
 */
export const buildId = process.env.COMMUNITY_BUILD_ID?.trim()
  || `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`

/**
 * Emits /version.json ({ build }) and /sw.js, a service worker that
 * precaches this build's hashed JS/CSS (see vite-plugins/sw-template.js).
 */
export function buildVersion(): Plugin {
  return {
    name: 'community:build-version',
    apply: 'build',
    async generateBundle(_options, bundle) {
      const precache = Object.keys(bundle)
        .filter((f) => f.startsWith('assets/') && /\.(js|css)$/.test(f))
        .sort()
        .map((f) => `/${f}`)
      const template = await readFile(path.resolve(import.meta.dirname, 'sw-template.js'), 'utf8')
      this.emitFile({
        type: 'asset',
        fileName: 'sw.js',
        source: template
          .replace('__BUILD_ID__', JSON.stringify(buildId))
          .replace('__PRECACHE__', JSON.stringify(precache)),
      })
      this.emitFile({ type: 'asset', fileName: 'version.json', source: JSON.stringify({ build: buildId }) + '\n' })
    },
  }
}
