import { expect, test } from '@playwright/test';

import { fileStartKey, loadPlayground, replaceSource } from './helpers';

// A comment that makes the language server's check spin the CPU for the given number of
// milliseconds when it runs with test hooks on, as it does under the dev server. See
// runTestHooks in cmd/lsp-server/recover.go.
const SPIN_MARKER = '// @escalier-lsp-test-spin';
const SPIN_MS = 8_000;

// How long a test allows the page to take over one interaction while the server spins.
const RESPONSIVE_MS = 1_000;

// How long after an edit the server is sure to be checking it. The editor sends a change
// 500 ms after the last keystroke.
const CHECK_STARTED_MS = 1_500;

test.describe('Slow check', () => {
    test.beforeEach(async ({ page }) => {
        await loadPlayground(page);
    });

    test('a slow check leaves typing, scrolling and the menus responsive', async ({
        page,
    }) => {
        const inputPanel = page.locator('#input-panel');
        const squiggles = inputPanel.locator('.squiggly-error');
        await expect(squiggles).toHaveCount(0);

        // The type error shows up only once the slow check finishes. Enough lines follow
        // it that the editor can scroll.
        const filler = Array.from(
            { length: 100 },
            (_, i) => `export val n${i} = ${i}`,
        );
        await replaceSource(page, [
            `${SPIN_MARKER} ${SPIN_MS}`,
            'export val x: number = "a"',
            ...filler,
        ]);
        // The interactions below have to land while the check spins, rather than before
        // the editor sends the change. Nothing on the page says a check has started, so
        // the test waits.
        await page.waitForTimeout(CHECK_STARTED_MS);

        // The longest the page goes without drawing a frame, from here until the test
        // reads it.
        await page.evaluate(() => {
            const w = window as unknown as { longestFrameGap: number };
            w.longestFrameGap = 0;
            let last = performance.now();
            const tick = (now: number) => {
                w.longestFrameGap = Math.max(w.longestFrameGap, now - last);
                last = now;
                requestAnimationFrame(tick);
            };
            requestAnimationFrame(tick);
        });

        // Typing shows up at once. The text goes in as one edit, since the server checks
        // each edit in turn and each check spins. It adds a second type error, at the top
        // of the file beside the first.
        await page.keyboard.press(fileStartKey);
        await page.keyboard.insertText('export val typed: number = "b"\n');
        await expect(inputPanel.locator('.view-lines')).toContainText(
            'export val typed',
            { timeout: RESPONSIVE_MS },
        );
        // The check hasn't reported the first type error yet.
        await expect(squiggles).toHaveCount(0);

        // Scrolling down moves the editor's scrollbar. Monaco draws the scrollbar itself,
        // so the test reads where its slider sits.
        const slider = inputPanel
            .locator('.scrollbar.vertical .slider')
            .first();
        const sliderTop = () =>
            slider.evaluate((el) => (el as HTMLElement).offsetTop);
        const before = await sliderTop();
        await inputPanel.locator('.view-lines').hover();
        await page.mouse.wheel(0, 2_000);
        await expect
            .poll(sliderTop, { timeout: RESPONSIVE_MS })
            .toBeGreaterThan(before);

        // A menu opens and closes.
        await page.getByRole('button', { name: 'Examples' }).click();
        const menu = page.getByRole('menu');
        await expect(menu).toBeVisible({ timeout: RESPONSIVE_MS });
        await page.getByRole('button', { name: 'Examples' }).click();
        await expect(menu).toBeHidden({ timeout: RESPONSIVE_MS });

        const longestFrameGap = await page.evaluate(
            () =>
                (window as unknown as { longestFrameGap: number })
                    .longestFrameGap,
        );
        expect(longestFrameGap).toBeLessThan(RESPONSIVE_MS);

        // The edit typed during the check is checked once the check ends, and that check
        // spins too. Its diagnostics hold both type errors. Monaco draws squiggles only on
        // the lines in view, and the menu took the focus from the editor.
        await inputPanel.locator('.view-lines').click();
        await page.keyboard.press(fileStartKey);
        await expect(squiggles).toHaveCount(2, { timeout: 4 * SPIN_MS });
    });
});
