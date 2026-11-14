import { expect, test } from '@playwright/test';

import { loadPlayground } from './helpers';

test.describe('Semantic tokens', () => {
    test.beforeEach(async ({ page }) => {
        await loadPlayground(page);
    });

    test('underlines a mutable binding and not an immutable one', async ({
        page,
    }) => {
        const inputPanel = page.locator('#input-panel');
        await inputPanel.locator('.view-lines').click();
        const modifier = process.platform === 'darwin' ? 'Meta' : 'Control';
        await page.keyboard.press(`${modifier}+A`);
        // insertText adds the text as one edit, so Monaco closes no brackets.
        await page.keyboard.insertText(
            [
                'export fn main() -> number {',
                '    val mut point = {x: 0}',
                '    point.x = 1',
                '    val fixed = point.x',
                '    return fixed',
                '}',
            ].join('\n'),
        );

        // Monaco gives an underlined token the class `mtku`.
        const underlined = inputPanel.locator('.view-lines span.mtku');
        await expect(underlined.filter({ hasText: /^point$/ })).toHaveCount(3, {
            timeout: 15_000,
        });
        await expect(underlined.filter({ hasText: /^fixed$/ })).toHaveCount(0);
    });

    test('bolds a var binding and bolds and underlines a var mut binding', async ({
        page,
    }) => {
        const inputPanel = page.locator('#input-panel');
        await inputPanel.locator('.view-lines').click();
        const modifier = process.platform === 'darwin' ? 'Meta' : 'Control';
        await page.keyboard.press(`${modifier}+A`);
        await page.keyboard.insertText(
            [
                'export fn main() -> number {',
                '    var count = 0',
                '    count = count + 1',
                '    var mut box = {x: 0}',
                '    box.x = count',
                '    return box.x',
                '}',
            ].join('\n'),
        );

        // Monaco gives a bold token the class `mtkb` and an underlined one `mtku`.
        const lines = inputPanel.locator('.view-lines');
        await expect(
            lines.locator('span.mtkb').filter({ hasText: /^count$/ }),
        ).toHaveCount(4, { timeout: 15_000 });
        await expect(
            lines.locator('span.mtku').filter({ hasText: /^count$/ }),
        ).toHaveCount(0);
        await expect(
            lines.locator('span.mtkb.mtku').filter({ hasText: /^box$/ }),
        ).toHaveCount(3);
    });
});
