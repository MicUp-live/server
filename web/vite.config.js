import { defineConfig } from 'vite'
import preact from '@preact/preset-vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [preact()],
  server: {
    host: '127.0.0.1',
    port: 3000,
    strictPort: true,
    allowedHosts: true,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true,
      },
      '/catalogs': {
        target: 'http://127.0.0.1:4566/micup-app-local',
        changeOrigin: true,
      },
    },
    hmr: {
      clientPort: 3000,
    },
  },
})
