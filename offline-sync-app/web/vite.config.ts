import path from 'node:path'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'
import { VitePWA } from 'vite-plugin-pwa'

// https://vite.dev/config/
export default defineConfig({
  // scripts/seed-cognito-local.sh writes one shared .env.local at the repo
  // root (also read by the Go backend) — point Vite at it instead of web/.
  envDir: path.resolve(import.meta.dirname, '..'),
  plugins: [
    react(),
    VitePWA({
      registerType: 'autoUpdate',
      manifest: {
        name: 'FieldSync',
        short_name: 'FieldSync',
        description: 'Offline-first field damage assessment',
        theme_color: '#1f2937',
        background_color: '#ffffff',
        display: 'standalone',
        icons: [],
      },
      workbox: {
        // Sync/mutation routes are handled by the app's own outbox, not the SW cache.
        navigateFallbackDenylist: [/^\/api/],
      },
    }),
  ],
})
