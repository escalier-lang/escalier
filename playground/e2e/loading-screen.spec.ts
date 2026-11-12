import { expect, test } from '@playwright/test';

import { COMPILE_TIMEOUT_MS, waitForCompilation } from './helpers';

test.describe('Loading screen', () => {
    test('shows download progress until the playground is ready', async ({
        page,
    }) => {
        const builtinRequests: string[] = [];
        page.on('request', (request) => {
            if (request.url().includes('/escalier-builtins/')) {
                builtinRequests.push(request.resourceType());
            }
        });

        await page.goto('/');

        const dialog = page.getByRole('dialog', {
            name: 'Loading the Escalier playground',
        });
        await expect(dialog).toBeVisible();
        await expect(dialog.getByRole('progressbar')).toBeAttached();

        await expect(dialog).toBeHidden({ timeout: COMPILE_TIMEOUT_MS });
        await waitForCompilation(page);

        // The loading screen fetches every standard library file before the language
        // server starts, so the filesystem never falls back to a synchronous request.
        expect(builtinRequests.length).toBeGreaterThan(0);
        expect(builtinRequests).not.toContain('xhr');
    });
});
