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
    }),
    AutoImport({
      resolvers: [TDesignResolver({ library: 'vue-next' })],
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
})
