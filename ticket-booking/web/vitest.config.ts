import { defineConfig } from 'vitest/config'

export default defineConfig({
  test: {
    environment: 'node', // pure model-layer logic — no DOM needed for these tests
    include: ['src/**/*.test.ts'],
  },
})
