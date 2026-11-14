import { type Page, expect, test } from '@playwright/test';

import { loadPlayground } from './helpers';

// replaceSource replaces the open file's text with lines.
async function replaceSource(page: Page, lines: string[]) {
    const inputPanel = page.locator('#input-panel');
    await inputPanel.locator('.view-lines').click();
    const modifier = process.platform === 'darwin' ? 'Meta' : 'Control';
    await page.keyboard.press(`${modifier}+A`);
    // insertText adds the text as one edit, so Monaco closes no brackets.
    await page.keyboard.insertText(lines.join('\n'));
}

test.describe('Hover', () => {
    test.beforeEach(async ({ page }) => {
        await loadPlayground(page);
    });

    test('expands and collapses a type alias', async ({ page }) => {
        await replaceSource(page, [
            'export type Point = {x: number, y?: number}',
            'export type Y = Point["y"]',
        ]);

        // The second line's `Y` is the name the alias declares.
        const name = page
            .locator('#input-panel .view-line')
            .nth(1)
            .locator('span > span', { hasText: /^Y$/ });
        // Monaco keeps a second, hidden hover element for its own tooltips.
        const hover = page.locator('.monaco-hover:visible');
        const increase = hover.locator('.codicon-hover-increase-verbosity');
        const decrease = hover.locator('.codicon-hover-decrease-verbosity');

        // The check runs after a debounce, so hover again until the hover
        // shows the alias. A single move onto the name does not open the
        // hover, so the mouse also moves within it.
        await expect(async () => {
            const box = await name.boundingBox();
            if (!box) {
                throw new Error('the alias name is not on screen');
            }
            const x = box.x + box.width / 2;
            const y = box.y + box.height / 2;
            await page.mouse.move(0, 0);
            await page.mouse.move(x, y);
            await page.mouse.move(x + 1, y);
            await expect(hover).toContainText('type Y = Point["y"]', {
                timeout: 2_000,
            });
        }).toPass({ timeout: 30_000 });
        await expect(hover).toContainText('= number');
        await expect(increase).toHaveClass(/enabled/);
        await expect(decrease).toHaveClass(/disabled/);

        await increase.click();
        await expect(hover).toContainText('type Y = number');
        await expect(hover).not.toContainText('Point');
        await expect(increase).toHaveClass(/disabled/);
        await expect(decrease).toHaveClass(/enabled/);

        await decrease.click();
        await expect(hover).toContainText('type Y = Point["y"]');
    });
});
