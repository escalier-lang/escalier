import { defineConfig } from 'vitest/config'

export default defineConfig({
  test: {
    coverage: {
      exclude: [
        '**/node_modules/**',
        '**/*.test.ts',
        'playground/**',
        'packages/vscode-escalier/**',
        'vitest.config.ts',
      ],
    }
  },
})
