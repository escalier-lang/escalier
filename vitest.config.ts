import { defineConfig } from 'vitest/config';

export default defineConfig({
    test: {
        coverage: {
            include: ['playground/src/**', 'packages/*/src/**'],
        },
        projects: [
            {
                extends: true,
                test: {
                    name: 'unit',
                    exclude: ['**/node_modules/**', '**/out/**'],
                },
            },
            'vitest.storybook.config.ts',
        ],
    },
});
