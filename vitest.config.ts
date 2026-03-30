import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { storybookTest } from '@storybook/addon-vitest/vitest-plugin';
import { playwright } from '@vitest/browser-playwright';
import { defineConfig } from 'vitest/config';

const dirname =
    typeof __dirname !== 'undefined'
        ? __dirname
        : path.dirname(fileURLToPath(import.meta.url));

export default defineConfig({
    test: {
        coverage: {
            include: ['playground/src/**', 'packages/*/src/**'],
        },
        projects: [
            {
                extends: true,
                resolve: {
                    alias: {
                        // monaco-editor-core has no `main`/`exports` fields,
                        // only `module`, so Node/SSR resolution fails.
                        // Alias it to the manual mock for unit tests.
                        'monaco-editor-core': path.join(
                            dirname,
                            'playground',
                            'src',
                            '__mocks__',
                            'monaco-editor-core.ts',
                        ),
                    },
                },
                test: {
                    name: 'unit',
                    exclude: ['**/node_modules/**', '**/out/**'],
                },
            },
            {
                extends: true,
                plugins: [
                    storybookTest({
                        configDir: path.join(
                            dirname,
                            'playground',
                            '.storybook',
                        ),
                    }),
                ],
                optimizeDeps: {
                    include: [
                        'react/jsx-dev-runtime',
                        'react',
                        'react-dom',
                        'react-dom/client',
                    ],
                },
                test: {
                    name: 'storybook',
                    root: path.join(dirname, 'playground'),
                    browser: {
                        enabled: true,
                        headless: true,
                        provider: playwright({}),
                        instances: [{ browser: 'chromium' }],
                    },
                },
            },
        ],
    },
});
