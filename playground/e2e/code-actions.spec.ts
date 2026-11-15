import { type Page, expect, test } from '@playwright/test';

import { loadPlayground } from './helpers';

// replaceSource replaces the open file's text with lines and leaves the cursor
// at the end of the last line.
async function replaceSource(page: Page, lines: string[]) {
    const inputPanel = page.locator('#input-panel');
    await inputPanel.locator('.view-lines').click();
    const modifier = process.platform === 'darwin' ? 'Meta' : 'Control';
    await page.keyboard.press(`${modifier}+A`);
    // insertText adds the text as one edit, so Monaco closes no brackets.
    await page.keyboard.insertText(lines.join('\n'));
}

test.describe('Code actions', () => {
    test.beforeEach(async ({ page }) => {
        await loadPlayground(page);
    });

    test('adds the arms a match is missing from the quick fix menu', async ({
        page,
    }) => {
        await replaceSource(page, [
            'export enum Shape { Circle(radius: number), Square(side: number) }',
            'export fn size(s: Shape) -> number throws _ {',
            '    return match s {',
            '        Shape.Circle(radius) => radius,',
            '    }',
            '}',
        ]);
        // The cursor goes to the line holding the `}` that closes the arms.
        await page.keyboard.press('ArrowUp');

        const inputPanel = page.locator('#input-panel');
        // A marker over several lines draws one squiggle per line.
        await expect(inputPanel.locator('.squiggly-error').first()).toBeVisible(
            {
                timeout: 15_000,
            },
        );

        await page.keyboard.press('Control+.');
        const action = page
            .locator('.action-widget')
            .getByText('Add missing match arms');
        await expect(action).toBeVisible({ timeout: 15_000 });
        await page.keyboard.press('Enter');

        await expect(inputPanel.locator('.view-lines')).toContainText(
            'Shape.Square(side) => throw "todo",',
        );
        await expect(inputPanel.locator('.squiggly-error')).toHaveCount(0, {
            timeout: 15_000,
        });
    });
});
