import { type Page, expect, test } from '@playwright/test';

import { COMPILE_TIMEOUT_MS, loadPlayground } from './helpers';

/** Returns the background colour of the input editor. */
function editorBackground(page: Page): Promise<string> {
    return page
        .locator('#input-panel .monaco-editor-background')
        .first()
        .evaluate((el) => getComputedStyle(el).backgroundColor);
}

test.describe('Theme', () => {
    test.use({ colorScheme: 'light' });

    test('follows the system theme until the user picks one', async ({
        page,
    }) => {
        await loadPlayground(page);
        const root = page.locator('html');

        await expect(root).toHaveAttribute('data-theme', 'light');
        // Monaco's light theme draws the editor in #fffffe.
        expect(await editorBackground(page)).toBe('rgb(255, 255, 254)');

        await page.emulateMedia({ colorScheme: 'dark' });
        await expect(root).toHaveAttribute('data-theme', 'dark');
        expect(await editorBackground(page)).toBe('rgb(30, 30, 30)');

        await page.getByRole('button', { name: 'Theme: System' }).click();
        await page.getByRole('menuitemradio', { name: 'Light' }).click();
        await expect(root).toHaveAttribute('data-theme', 'light');

        // The choice outlasts a change to the system theme and a reload.
        await page.emulateMedia({ colorScheme: 'dark' });
        await page.reload();
        await expect(root).toHaveAttribute('data-theme', 'light');
        // The toolbar shows once the reloaded page finishes starting up, which can
        // take longer than the default timeout on a loaded runner.
        await expect(
            page.getByRole('button', { name: 'Theme: Light' }),
        ).toBeVisible({ timeout: COMPILE_TIMEOUT_MS });
    });
});
