import { execSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { readFile } from 'node:fs/promises'
import path from 'node:path'
import type { Plugin } from 'vite'

/**
 * Build id shared by the client bundle (__BUILD_ID__), /version.json and the
 * service worker: <git sha>-<build time>, so every build is distinct and
 * traceable to a commit. Override with COMMUNITY_BUILD_ID (whole id) or
 * COMMUNITY_BUILD_SHA (sha part, for builds outside a git checkout).
 */
function gitSha(): string {
  try {
    return execSync('git rev-parse --short=12 HEAD', { stdio: ['ignore', 'pipe', 'ignore'] }).toString().trim()
  } catch {
    return ''
  }
}
export const buildSha = process.env.COMMUNITY_BUILD_SHA?.trim() || gitSha()
export const builtAt = new Date().toISOString()
export const buildId = process.env.COMMUNITY_BUILD_ID?.trim()
  || `${buildSha || 'nogit'}-${Date.now().toString(36)}`

/**
 * Emits /version.json ({ build }) and /sw.js, a service worker that
 * precaches this build's hashed JS/CSS (see vite-plugins/sw-template.js).
 */
export function buildVersion(): Plugin {
  return {
    name: 'community:build-version',
    apply: 'build',
    // Non-hashed files named by index.html (logo, manifest, icons,
    // theme-init.js) get ?v=<build id>. Vite-hashed /assets files are left
    // alone so unchanged chunks stay cached across deploys.
    transformIndexHtml: {
      order: 'post',
      handler(html) {
        return html.replace(/(\s(?:href|src)=")(\/(?!assets\/)[^"?#]+)(")/g, (_m, pre: string, url: string, post: string) =>
          `${pre}${url}?v=${encodeURIComponent(buildId)}${post}`)
      },
    },
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
      this.emitFile({ type: 'asset', fileName: 'version.json', source: JSON.stringify({ build: buildId, sha: buildSha || null, built_at: builtAt }) + '\n' })
    },
  }
}

/**
 * public/ files referenced from CSS (fonts) get ?v=<content hash of the
 * file>, not the build id, so the stylesheet chunk keeps its hash (and stays
 * cached) across deploys that do not change the font. Runs before Vite's
 * CSS pipeline.
 */
export function versionPublicCssUrls(): Plugin {
  let publicDir = ''
  return {
    name: 'community:version-public-css-urls',
    apply: 'build',
    enforce: 'pre',
    configResolved(config) { publicDir = config.publicDir },
    transform(code, id) {
      if (!/\.(css|scss|vue)(\?|$)/.test(id) || !code.includes('/fonts/')) return null
      return code.replace(/url\((["']?)(\/fonts\/[^"')?#]+)\1\)/g, (m, q: string, url: string) => {
        try {
          const hash = createHash('sha256').update(readFileSync(path.join(publicDir, url))).digest('hex').slice(0, 10)
          return `url(${q}${url}?v=${hash}${q})`
        } catch {
          return m
        }
      })
    },
  }
}
