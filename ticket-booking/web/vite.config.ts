import path from 'node:path'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// https://vite.dev/config/
export default defineConfig({
  // scripts/seed-cognito-local.sh writes one shared .env.local at the repo
  // root (also read by the Go backend) — point Vite at it instead of web/.
  // No vite-plugin-pwa here (unlike the sibling project): this app is not
  // offline-first, and a service worker fighting the layout bucket's
  // 1-year-immutable Cache-Control is a bug generator, not a feature
  // (docs/plan.md "Frontend" section).
  envDir: path.resolve(import.meta.dirname, '..'),
  plugins: [react()],
})
