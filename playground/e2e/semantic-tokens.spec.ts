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
});
