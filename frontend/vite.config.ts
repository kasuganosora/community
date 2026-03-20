import { defineConfig } from 'vite'
import path from 'path'
import vue from '@vitejs/plugin-vue'
import Components from 'unplugin-vue-components/vite'
import AutoImport from 'unplugin-auto-import/vite'
import { TDesignResolver } from 'unplugin-vue-components/resolvers'

export default defineConfig({
  plugins: [
    vue(),
    Components({
      resolvers: [TDesignResolver({ library: 'vue-next' })],
      dts: true, // Generate TypeScript declaration
    }),
    AutoImport({
      resolvers: [TDesignResolver({ library: 'vue-next' })],
      imports: ['vue', 'vue-router', 'pinia'],
      dts: true,
    }),
  ],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, 'src'),
    },
  },
  server: {
    port: 3000,
    proxy: {
      '/api': 'http://localhost:9090',
      '/oauth': 'http://localhost:9090',
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
      output: {
        // Manual chunk splitting - simplified for compatibility
        manualChunks: {
          // Vendor chunks
          'vendor-vue': ['vue', 'vue-router', 'pinia'],
          'vendor-tdesign': ['tdesign-vue-next'],
          'vendor-utils': ['axios', 'dompurify'],
        },
        // Chunk naming
        chunkFileNames: 'assets/js/[name]-[hash].js',
        entryFileNames: 'assets/js/[name]-[hash].js',
        assetFileNames: (assetInfo) => {
          const info = assetInfo.name || ''
          if (info.endsWith('.css')) {
            return 'assets/css/[name]-[hash][extname]'
          }
          if (info.endsWith('.png') || info.endsWith('.jpg') || info.endsWith('.jpeg') || info.endsWith('.gif') || info.endsWith('.svg')) {
            return 'assets/images/[name]-[hash][extname]'
          }
          return 'assets/[name]-[hash][extname]'
        },
      },
    },

    // Minification options
    minify: 'esbuild',

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
})
