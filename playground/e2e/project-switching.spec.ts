import { type Page, expect, test } from '@playwright/test';

import { loadPlayground, waitForCompilation } from './helpers';

/** Appends a comment to the open file, the way a person editing the project would. */
async function editOpenFile(page: Page): Promise<void> {
    const inputPanel = page.locator('#input-panel');
    await inputPanel.locator('.view-lines').click();
    const modifier = process.platform === 'darwin' ? 'Meta' : 'Control';
    await page.keyboard.press(`${modifier}+End`);
    await page.keyboard.type('\n// edited');
    // Keyboard events can be dropped or delayed under parallel execution.
    await expect(inputPanel.locator('.view-lines')).toContainText('edited', {
        timeout: 5_000,
    });
}

/** Picks the Calculator example from the Examples menu. */
async function pickCalculator(page: Page): Promise<void> {
    await page.getByRole('button', { name: 'Examples' }).click();
    await page.getByRole('menuitem', { name: 'Calculator' }).click();
}

test.describe('Project Switching', () => {
    test.beforeEach(async ({ page }) => {
        await loadPlayground(page);
    });

    test('switches an unedited project without asking', async ({ page }) => {
        await pickCalculator(page);

        await expect(page.getByRole('dialog')).toBeHidden();
        await expect(page.getByText('math.esc')).toBeVisible({
            timeout: 10_000,
        });
        await waitForCompilation(page);
    });

    test('asks before replacing an edited project', async ({ page }) => {
        await editOpenFile(page);
        await pickCalculator(page);

        const dialog = page.getByRole('dialog');
        await expect(dialog).toBeVisible();
        await expect(dialog).toContainText('Replace current project');

        await dialog.getByRole('button', { name: 'Replace' }).click();

        await expect(page.getByText('math.esc')).toBeVisible({
            timeout: 10_000,
        });
        await waitForCompilation(page);
    });

    test('asks again after switching once the new project is edited', async ({
        page,
    }) => {
        await pickCalculator(page);
        await expect(page.getByText('math.esc')).toBeVisible({
            timeout: 10_000,
        });

        await editOpenFile(page);
        await page.getByRole('button', { name: 'Examples' }).click();
        await page.getByRole('menuitem', { name: 'Hello world' }).click();

        await expect(page.getByRole('dialog')).toBeVisible();
    });

    test('cancel project switch preserves current state', async ({ page }) => {
        await editOpenFile(page);
        await pickCalculator(page);

        const dialog = page.getByRole('dialog');
        await dialog.getByRole('button', { name: 'Cancel' }).click();

        // Hello-world files should still be visible (no math.esc), with the edit kept.
        await expect(page.getByText('math.esc')).toBeHidden();
        await expect(page.getByText('escalier.toml')).toBeVisible();
        await expect(
            page.locator('#input-panel').locator('.view-lines'),
        ).toContainText('edited');
    });

    test('switching examples auto-opens build output on right side', async ({
        page,
    }) => {
        const outputTablist = page.getByTestId('output-tablist');

        // After initial load, the right tablist should have index.js.
        // The auto-open races with React effect setup so allow extra time.
        await expect(
            outputTablist.getByRole('tab', { name: /index\.js/ }),
        ).toBeVisible({ timeout: 10_000 });

        await pickCalculator(page);

        // Wait for the new project to compile
        await waitForCompilation(page);

        // The right tablist should have the new build output auto-opened
        await expect(
            outputTablist.getByRole('tab', { name: /index\.js/ }),
        ).toBeVisible({ timeout: 10_000 });
    });

    test('URL updates when switching examples', async ({ page }) => {
        await pickCalculator(page);
        await waitForCompilation(page);

        // URL should contain the example query param
        // history.replaceState is used, so we need to evaluate in page context
        await expect(async () => {
            const url = await page.evaluate(() => window.location.search);
            expect(url).toContain('example=calculator');
        }).toPass();
    });
});
