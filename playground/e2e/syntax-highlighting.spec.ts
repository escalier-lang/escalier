import { expect, test } from '@playwright/test';

import { loadPlayground } from './helpers';

test.describe('Syntax highlighting', () => {
    test.beforeEach(async ({ page }) => {
        await loadPlayground(page);
    });

    test('colours get, set and mut as keywords and -> as one operator', async ({
        page,
    }) => {
        const inputPanel = page.locator('#input-panel');
        await inputPanel.locator('.view-lines').click();
        const modifier = process.platform === 'darwin' ? 'Meta' : 'Control';
        await page.keyboard.press(`${modifier}+A`);
        // insertText adds the text as one edit, so Monaco closes no brackets.
        await page.keyboard.insertText(
            [
                'class Box {',
                '    value: number,',
                '    get doubled(&self) -> number { return self.value * 2 },',
                '    set total(&mut self, n: number) { self.value = n },',
                '}',
                'val mut map = new Map<string, number>()',
                'val found = map.get("a")',
            ].join('\n'),
        );

        const lines = inputPanel.locator('.view-lines');
        // Monaco draws each token as a span whose class names its colour.
        const spans = (text: string) =>
            lines
                .locator('span > span')
                .filter({ hasText: new RegExp(`^${text}$`) });
        const keywordClass =
            (await spans('class').first().getAttribute('class')) ?? '';
        expect(keywordClass).not.toBe('');

        for (const keyword of ['get', 'set', 'mut']) {
            await expect(spans(keyword).first()).toHaveAttribute(
                'class',
                keywordClass,
            );
        }
        // The `get` in `map.get("a")` names a method, so it keeps the identifier
        // colour. Monaco joins it with the `.` and `(` around it when the theme
        // draws them in the same colour, so match any span that contains it.
        const methodGet = lines
            .locator('.view-line')
            .filter({ hasText: 'val found' })
            .locator('span > span')
            .filter({ hasText: 'get' });
        await expect(methodGet).toHaveCount(1);
        await expect(methodGet).not.toHaveAttribute('class', keywordClass);

        // Bracket pair colouring gives a bracket its own class, so a `->` drawn in
        // one colour is a single span with no bracket class. Monaco joins the spaces
        // around it into that span because they share its colour.
        const arrow = lines
            .locator('span > span')
            .filter({ hasText: /^\s*->\s*$/ });
        await expect(arrow).toHaveCount(1);
        await expect(arrow).not.toHaveClass(/bracket-highlighting/);
    });

    test('colours keywords in compiled JavaScript', async ({ page }) => {
        // The playground opens the compiled build output in the output panel.
        const lines = page.locator('#output-panel .view-lines');
        await expect(lines).toContainText('function');

        const spans = (text: string) =>
            lines
                .locator('span > span')
                .filter({ hasText: new RegExp(`^${text}$`) });
        const keywordClass =
            (await spans('function').first().getAttribute('class')) ?? '';
        expect(keywordClass).not.toBe('');
        await expect(spans('export').first()).toHaveAttribute(
            'class',
            keywordClass,
        );

        // hello-world's `Language.English() => "Hello"` compiles to a string literal,
        // which gets a colour of its own.
        const hello = spans('["\']Hello["\']').first();
        await expect(hello).toBeVisible();
        await expect(hello).not.toHaveAttribute('class', keywordClass);
    });
});
