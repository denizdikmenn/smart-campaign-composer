import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// API_PROXY_TARGET is where /api requests are forwarded in dev.
// In Docker Compose it points at the backend service.
const target = process.env.API_PROXY_TARGET ?? 'http://localhost:8080'

export default defineConfig({
  plugins: [vue()],
  server: {
    host: true,
    port: 5173,
    proxy: { '/api': { target, changeOrigin: true } },
  },
})
