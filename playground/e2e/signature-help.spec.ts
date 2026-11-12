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

test.describe('Signature help', () => {
    test.beforeEach(async ({ page }) => {
        await loadPlayground(page);
    });

    test('shows a function signature and follows the active parameter', async ({
        page,
    }) => {
        await replaceSource(page, [
            'export fn add(a: number, b: number) -> number { return a + b }',
            '',
            'export val x = ',
        ]);
        await page.keyboard.type('add(');

        const widget = page.locator('.parameter-hints-widget');
        await expect(widget).toBeVisible({ timeout: 15_000 });
        await expect(widget.locator('.signature')).toHaveText(
            'add(a: number, b: number) -> number',
        );
        await expect(widget.locator('.parameter.active')).toHaveText(
            'a: number',
        );

        await page.keyboard.type('1, ');
        await expect(widget.locator('.parameter.active')).toHaveText(
            'b: number',
            { timeout: 15_000 },
        );
    });

    test('lists every constructor of a class', async ({ page }) => {
        await replaceSource(page, [
            'import "std:intl"',
            '',
            'export val c = intl.Collator',
        ]);
        // Close the completion list the `.` may have opened, so the `(` goes
        // into the document.
        await page.keyboard.press('Escape');
        await page.keyboard.type('(');

        const widget = page.locator('.parameter-hints-widget');
        await expect(widget).toBeVisible({ timeout: 15_000 });
        await expect(widget.locator('.signature')).toHaveText(
            'Collator(locales?: LocalesArgument, options?: CollatorOptions) -> Collator',
        );
        await expect(widget.locator('.overloads')).toHaveText('1/2');
    });
});
