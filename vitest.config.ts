import { defineConfig } from 'vitest/config';

export default defineConfig({
    test: {
        exclude: ['**/node_modules/**', '**/out/**'],
        coverage: {
            exclude: [
                '**/node_modules/**',
                '**/examples/**',
                '**/fixtures/**',
                '**/*.test.ts',
                'playground/**',
                'packages/vscode-escalier/**',
                'vitest.config.ts',
            ],
        },
    },
});
