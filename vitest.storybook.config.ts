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
    optimizeDeps: {
        include: [
            'react/jsx-dev-runtime',
            'react',
            'react-dom',
            'react-dom/client',
        ],
    },
    plugins: [
        storybookTest({
            configDir: path.join(dirname, 'playground', '.storybook'),
        }),
    ],
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
});
