import { defineConfig, loadEnv } from 'vite'
import path from 'path'
import vue from '@vitejs/plugin-vue'
import Components from 'unplugin-vue-components/vite'
import AutoImport from 'unplugin-auto-import/vite'
import { TDesignResolver } from 'unplugin-vue-components/resolvers'
import { precompress } from './vite-plugins/precompress.ts'

export default defineConfig(({ mode }) => {
  // Dev-server proxy target for the Go backend. Defaults to the backend's
  // default LISTEN (:8080); override with VITE_DEV_PROXY_TARGET in
  // frontend/.env.local or the environment.
  const env = loadEnv(mode, process.cwd(), '')
  const proxyTarget = env.VITE_DEV_PROXY_TARGET || 'http://localhost:8080'

  return {
    plugins: [
      vue(),
      Components({
        // <t-icon> resolves to our AppIcon (inline SVG, CSP-safe) before the
        // TDesign resolver would map it to the CDN-backed sprite icon.
        resolvers: [
          (name: string) => (name === 'TIcon'
            ? { name: 'default', from: '@/shared/components/common/AppIcon.vue' }
            : undefined),
          // <t-loading> is the only TDesign component on the first screen;
          // a local spinner keeps TDesign's shared runtime out of it.
          (name: string) => (name === 'TLoading'
            ? { name: 'default', from: '@/shared/components/common/AppLoading.vue' }
            : undefined),
          // <t-button> is on nearly every screen; a local button with the
          // same class names drops TDesign's Button runtime and stylesheet.
          (name: string) => (name === 'TDialog'
            ? { name: 'default', from: '@/shared/components/common/LazyDialog.vue' }
            : undefined),
          (name: string) => (name === 'TButton'
            ? { name: 'default', from: '@/shared/components/common/AppButton.vue' }
            : undefined),
          TDesignResolver({ library: 'vue-next' }),
        ],
        dts: 'src/components.d.ts', // Generate TypeScript declaration
      }),
      precompress(),
      AutoImport({
        resolvers: [TDesignResolver({ library: 'vue-next' })],
        imports: ['vue', 'vue-router', 'pinia'],
        dts: 'auto-imports.d.ts',
      }),
    ],
    resolve: {
      alias: {
        '@': path.resolve(import.meta.dirname, 'src'),
      },
    },
    server: {
      port: 3000,
      proxy: {
        '/api': { target: proxyTarget, ws: true },
        '/oauth': proxyTarget,
        '/media': proxyTarget,
        '/.well-known': proxyTarget,
      },
    },
    css: {
      preprocessorOptions: {
        scss: {},
      },
    },
    build: {
      // Chunk size warnings
      chunkSizeWarningLimit: 500,

      // Code splitting configuration
      rollupOptions: {
        // The setup wizard is a separate, small entry served only before install.
        input: {
          main: path.resolve(import.meta.dirname, 'index.html'),
          setup: path.resolve(import.meta.dirname, 'setup.html'),
        },
        output: {
          // Manual chunk splitting
          manualChunks(id) {
            if (id.includes('node_modules')) {
              if (id.includes('vue-router') || id.includes('pinia') || id.includes('/vue/') || id.includes('/@vue/')) {
                return 'vendor-vue'
              }
              // tdesign-vue-next is not forced into one chunk: components
              // are imported on demand by the resolver and the plugins lazily
              // (shared/ui/feedback.ts), so the first screen loads none of it.
              if (id.includes('axios') || id.includes('dompurify')) {
                return 'vendor-utils'
              }
            }
          },
          // Chunk naming
          chunkFileNames: 'assets/js/[name]-[hash].js',
          entryFileNames: 'assets/js/[name]-[hash].js',
          assetFileNames: (assetInfo) => {
            const names = assetInfo.names ?? []
            const name = names.length > 0 ? names[names.length - 1] : (assetInfo.name ?? '')
            if (name.endsWith('.css')) {
              return 'assets/css/[name]-[hash][extname]'
            }
            if (name.endsWith('.png') || name.endsWith('.jpg') || name.endsWith('.jpeg') || name.endsWith('.gif') || name.endsWith('.svg')) {
              return 'assets/images/[name]-[hash][extname]'
            }
            return 'assets/[name]-[hash][extname]'
          },
        },
      },

      // Minification options
      minify: true,
      cssMinify: true,

      // Source maps for production (can be disabled for smaller builds)
      sourcemap: false,

      // CSS optimization
      cssCodeSplit: true,

      // Assets
      assetsInlineLimit: 4096, // Inline assets smaller than 4kb
    },

    // Optimize dependencies
    optimizeDeps: {
      include: [
        'vue',
        'vue-router',
        'pinia',
        'tdesign-vue-next',
        'axios',
        'dompurify',
      ],
      exclude: [],
    },
  }
})
