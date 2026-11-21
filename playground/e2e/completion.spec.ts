import { expect, test } from '@playwright/test';

import { loadPlayground, replaceSource } from './helpers';

test.describe('Completion', () => {
    test.beforeEach(async ({ page }) => {
        await loadPlayground(page);
    });

    test('inserts `throws _` after a return type and selects the `_`', async ({
        page,
    }) => {
        await replaceSource(page, ['export fn f() -> number ']);
        await page.keyboard.type('th');
        await page.keyboard.press('Control+Space');

        const widget = page.locator('.suggest-widget');
        const item = widget.getByRole('option', { name: /throws/ });
        await expect(item).toBeVisible({ timeout: 15_000 });
        await page.keyboard.press('Enter');

        // Typing replaces the selected placeholder, so the `_` is gone.
        await page.keyboard.type('string');
        // The file holds one line, so the text of every rendered line is that line.
        // Monaco renders each space in a line as a non-breaking space.
        const lines = page.locator('#input-panel .view-lines');
        await expect(lines).toHaveText(
            'export fn f() -> number throws string'.replaceAll(' ', '\u00a0'),
        );
    });
});
