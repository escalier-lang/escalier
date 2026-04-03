import { expect, test } from '@playwright/test';

import { loadPlayground } from './helpers';

test.describe('App Loading', () => {
    test('default load shows hello-world example', async ({ page }) => {
        await loadPlayground(page);

        // Left side should have index.esc tab open (lib/index.esc is the primary file)
        const inputTablist = page.getByRole('tablist').first();
        await expect(
            inputTablist.getByRole('tab', { name: /index\.esc/ }),
        ).toBeVisible();

        // File explorer should show the hello-world project structure
        await expect(
            page.getByRole('button', { name: /^▸ bin$|^▾ bin$/ }),
        ).toBeVisible();
        await expect(
            page.getByRole('button', { name: /^▸ lib$|^▾ lib$/ }),
        ).toBeVisible();
        await expect(page.getByText('escalier.toml')).toBeVisible();
        await expect(page.getByText('package.json')).toBeVisible();

        // Build directory should exist (compilation succeeded)
        await expect(page.getByRole('button', { name: /build/ })).toBeVisible();
    });

    test('deep link to calculator example', async ({ page }) => {
        await loadPlayground(page, { example: 'calculator' });

        // Should have index.esc tab (primary file)
        const inputTablist = page.getByRole('tablist').first();
        await expect(
            inputTablist.getByRole('tab', { name: /index\.esc/ }),
        ).toBeVisible();

        // File explorer should show calculator-specific file
        await expect(page.getByText('math.esc')).toBeVisible();
    });

    test('invalid example falls back to hello-world with warning', async ({
        page,
    }) => {
        await page.goto('/?example=nonexistent');

        // Should show a warning toast about unknown example
        await expect(page.getByRole('alert').first()).toContainText(
            'Unknown example',
        );

        // Should still load and compile successfully (hello-world fallback)
        await expect(page.getByRole('button', { name: /build/ })).toBeVisible({
            timeout: 45_000,
        });

        // File explorer should show hello-world structure
        await expect(page.getByText('escalier.toml')).toBeVisible();
    });

    test('compilation produces build output', async ({ page }) => {
        await page.goto('/', { waitUntil: 'networkidle' });

        // The build directory should eventually appear after compilation
        await expect(page.getByRole('button', { name: /build/ })).toBeVisible({
            timeout: 45_000,
        });
    });
});
