import { expect, test } from '@playwright/test';

import { loadPlayground, waitForCompilation } from './helpers';

test.describe('Project Switching', () => {
    test.beforeEach(async ({ page }) => {
        await loadPlayground(page);
    });

    test('switch to calculator example', async ({ page }) => {
        // Open the Examples dropdown
        await page.getByRole('button', { name: 'Examples' }).click();

        // Click Calculator
        await page.getByRole('menuitem', { name: 'Calculator' }).click();

        // Confirmation dialog should appear
        const dialog = page.getByRole('dialog');
        await expect(dialog).toBeVisible();
        await expect(dialog).toContainText('Replace current project');

        // Confirm the replacement
        await dialog.getByRole('button', { name: 'Replace' }).click();

        // Wait for the new project to compile
        await waitForCompilation(page);

        // Calculator should have math.esc in the explorer
        await expect(page.getByText('math.esc')).toBeVisible();
    });

    test('cancel project switch preserves current state', async ({ page }) => {
        // Open the Examples dropdown
        await page.getByRole('button', { name: 'Examples' }).click();

        // Click Calculator
        await page.getByRole('menuitem', { name: 'Calculator' }).click();

        // Cancel in the dialog
        const dialog = page.getByRole('dialog');
        await dialog.getByRole('button', { name: 'Cancel' }).click();

        // Hello-world files should still be visible (no math.esc)
        await expect(page.getByText('math.esc')).toBeHidden();
        await expect(page.getByText('escalier.toml')).toBeVisible();
    });

    test('URL updates when switching examples', async ({ page }) => {
        // Switch to calculator
        await page.getByRole('button', { name: 'Examples' }).click();
        await page.getByRole('menuitem', { name: 'Calculator' }).click();
        await page
            .getByRole('dialog')
            .getByRole('button', { name: 'Replace' })
            .click();
        await waitForCompilation(page);

        // URL should contain the example query param
        // history.replaceState is used, so we need to evaluate in page context
        await expect(async () => {
            const url = await page.evaluate(() => window.location.search);
            expect(url).toContain('example=calculator');
        }).toPass();
    });
});
