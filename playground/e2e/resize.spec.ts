import { type Page, expect, test } from '@playwright/test';

import { loadPlayground } from './helpers';

const PANES = ['#input-panel', '#output-panel'] as const;

/** How far, in pixels, an editor's size may drift from its pane's. */
const TOLERANCE_PX = 2;

type Size = { width: number; height: number };

/** Returns the size of a pane and of the Monaco editor inside it. */
async function paneAndEditorSizes(
    page: Page,
    pane: string,
): Promise<{ pane: Size; editor: Size }> {
    return page.evaluate((selector) => {
        const size = (el: Element | null) => {
            if (!el) throw new Error(`missing element under ${selector}`);
            const { width, height } = el.getBoundingClientRect();
            return { width, height };
        };
        const paneElem = document.querySelector(selector);
        return {
            pane: size(paneElem),
            editor: size(paneElem?.querySelector('.monaco-editor') ?? null),
        };
    }, pane);
}

/**
 * Waits until each editor fills its pane, and returns the pane sizes. Monaco lays out
 * from a ResizeObserver callback, so the editor can lag a viewport change by a frame.
 */
async function expectEditorsFillPanes(page: Page): Promise<Size[]> {
    const sizes: Size[] = [];
    for (const pane of PANES) {
        await expect
            .poll(async () => {
                const s = await paneAndEditorSizes(page, pane);
                return (
                    Math.abs(s.pane.width - s.editor.width) <= TOLERANCE_PX &&
                    Math.abs(s.pane.height - s.editor.height) <= TOLERANCE_PX
                );
            })
            .toBe(true);
        sizes.push((await paneAndEditorSizes(page, pane)).pane);
    }
    return sizes;
}

/** Returns how far the page scrolls past the viewport in each direction. */
async function pageOverflow(page: Page): Promise<Size> {
    return page.evaluate(() => {
        const root = document.documentElement;
        return {
            width: root.scrollWidth - root.clientWidth,
            height: root.scrollHeight - root.clientHeight,
        };
    });
}

test.describe('Window resize', () => {
    test.beforeEach(async ({ page }) => {
        await page.setViewportSize({ width: 1400, height: 900 });
        await loadPlayground(page);
        // The output editor appears once the first compile opens a build file there.
        await expect(
            page.locator('#output-panel .monaco-editor'),
        ).toBeVisible();
    });

    test('editors follow the window as it shrinks and grows', async ({
        page,
    }) => {
        // Each step changes both dimensions, so a pane stuck at an earlier size fails.
        const viewports: Size[] = [
            { width: 1400, height: 900 },
            { width: 900, height: 600 },
            { width: 360, height: 400 },
            { width: 1600, height: 1000 },
            { width: 1000, height: 700 },
        ];
        for (const viewport of viewports) {
            await page.setViewportSize(viewport);
            const [input, output] = await expectEditorsFillPanes(page);

            // The grid is 220px of explorer plus two equal editor columns, and 36px of
            // toolbar plus 40px of tabs above the editors and 24px of status bar below.
            const paneWidth = (viewport.width - 220) / 2;
            const paneHeight = viewport.height - 36 - 40 - 24;
            for (const pane of [input, output]) {
                expect(Math.abs(pane.width - paneWidth)).toBeLessThanOrEqual(
                    TOLERANCE_PX,
                );
                expect(Math.abs(pane.height - paneHeight)).toBeLessThanOrEqual(
                    TOLERANCE_PX,
                );
            }
            expect(await pageOverflow(page)).toEqual({ width: 0, height: 0 });
        }
    });

    test('a narrow window keeps the editors inside their panes', async ({
        page,
    }) => {
        await page.setViewportSize({ width: 360, height: 500 });
        await expectEditorsFillPanes(page);

        for (const pane of PANES) {
            const box = await page.locator(pane).boundingBox();
            const textArea = await page
                .locator(`${pane} .monaco-editor .overflow-guard`)
                .boundingBox();
            if (!box || !textArea) {
                throw new Error(`${pane} or its editor has no bounding box`);
            }
            expect(textArea.x + textArea.width).toBeLessThanOrEqual(
                box.x + box.width + TOLERANCE_PX,
            );
        }
        expect(await pageOverflow(page)).toEqual({ width: 0, height: 0 });
    });
});
