import { expect, test } from '@playwright/test';

import { loadPlayground } from './helpers';

test.describe('Tab Management', () => {
    test.beforeEach(async ({ page }) => {
        await loadPlayground(page);
    });

    test('close tab via close button', async ({ page }) => {
        const inputTablist = page.getByTestId('input-tablist');

        // index.esc tab should be open (default primary file)
        await expect(
            inputTablist.getByRole('tab', { name: /index\.esc/ }),
        ).toBeVisible();

        // Hover over the tab to reveal the close button, then click it
        await inputTablist.getByRole('tab', { name: /index\.esc/ }).hover();
        await page.getByLabel('Close index.esc').click();

        // Tab should be gone
        await expect(
            inputTablist.getByRole('tab', { name: /index\.esc/ }),
        ).toBeHidden();
    });

    test('switch between tabs', async ({ page }) => {
        // Open a second file
        await page.getByRole('button', { name: 'main.esc' }).click();

        const inputTablist = page.getByTestId('input-tablist');
        const indexTab = inputTablist.getByRole('tab', { name: /index\.esc/ });
        const mainTab = inputTablist.getByRole('tab', { name: /main\.esc/ });

        // main.esc should now be the active tab (just clicked it)
        await expect(mainTab).toHaveAttribute('aria-selected', 'true');
        await expect(indexTab).toHaveAttribute('aria-selected', 'false');

        // Click index.esc tab to switch back
        await indexTab.click();
        await expect(indexTab).toHaveAttribute('aria-selected', 'true');
        await expect(mainTab).toHaveAttribute('aria-selected', 'false');
    });

    test('move tab to right side via context menu', async ({ page }) => {
        const inputTablist = page.getByTestId('input-tablist');
        const outputTablist = page.getByTestId('output-tablist');

        // Right-click on the index.esc tab
        await inputTablist
            .getByRole('tab', { name: /index\.esc/ })
            .click({ button: 'right' });

        // Click "Move to Right" in the context menu
        await page.getByRole('button', { name: 'Move to Right' }).click();

        // Tab should move from left to right tablist
        await expect(
            inputTablist.getByRole('tab', { name: /index\.esc/ }),
        ).toBeHidden();
        await expect(
            outputTablist.getByRole('tab', { name: /index\.esc/ }),
        ).toBeVisible();
    });
});
