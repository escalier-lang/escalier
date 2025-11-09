import { defineConfig } from 'vitest/config';

export default defineConfig({
    test: {
        exclude: ['**/node_modules/**', '**/out/**'],
        coverage: {
            include: ['playground/src/lsp-client/**', 'packages/*/src/**'],
        },
    },
});
