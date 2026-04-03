import { expect, test } from '@playwright/test';

import { loadPlayground } from './helpers';

test.describe('Editor', () => {
    test.beforeEach(async ({ page }) => {
        await loadPlayground(page);
    });

    test('left editor shows source code', async ({ page }) => {
        // The input panel should contain Escalier source from hello-world's index.esc
        const inputPanel = page.locator('#input-panel');
        // hello-world's index.esc contains a greet function
        await expect(inputPanel.locator('.view-lines')).toContainText('greet');
    });

    test('compiled output contains JavaScript', async ({ page }) => {
        // Open a build output file via the file explorer.
        // The explorer always opens files on the left (input) side.
        await page.getByRole('button', { name: /^[▸▾] build$/ }).click();
        await page
            .getByRole('button', { name: /^[▸▾] lib$/ })
            .nth(1)
            .click();
        await page
            .getByRole('button', { name: 'index.js', exact: true })
            .click();

        // The input panel should now show the compiled JavaScript.
        // Monaco may take a moment to render after switching models.
        const inputPanel = page.locator('#input-panel');
        await expect(inputPanel.locator('.view-lines')).toContainText(
            'function',
            { timeout: 10_000 },
        );
    });

    test('editing source triggers recompilation', async ({ page }) => {
        // Click into the editor content area and navigate to end of file
        const inputPanel = page.locator('#input-panel');
        await inputPanel.locator('.view-lines').click();
        const modifier = process.platform === 'darwin' ? 'Meta' : 'Control';
        await page.keyboard.press(`${modifier}+End`);
        await page.keyboard.type(
            '\n\nexport fn added() -> string { return "hi" }',
        );

        // Open the build output to verify recompilation produced new content.
        // Expand build/lib/ and open index.js.
        await page.getByRole('button', { name: /^[▸▾] build$/ }).click();
        await page
            .getByRole('button', { name: /^[▸▾] lib$/ })
            .nth(1)
            .click();

        // Poll for the `added` function to appear in the compiled output
        const outputPanel = page.locator('#output-panel');
        await page
            .getByRole('button', { name: 'index.js', exact: true })
            .click();

        await expect(async () => {
            const content = await outputPanel
                .locator('.view-lines')
                .textContent();
            expect(content).toContain('added');
        }).toPass({ timeout: 30_000 });
    });
});
