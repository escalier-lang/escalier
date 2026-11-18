import { type Page, expect, test } from '@playwright/test';

import { loadPlayground, recordAlerts, recordedAlerts } from './helpers';

// The modifier Monaco's shortcuts use on this platform.
const modifier = process.platform === 'darwin' ? 'Meta' : 'Control';

// Comments that crash the language server when it runs with test hooks on, as it does
// under the dev server. See runTestHooks in cmd/lsp-server/recover.go.
const PANIC_MARKER = '// @escalier-lsp-test-panic';
const CRASH_MARKER = '// @escalier-lsp-test-crash';

// A new language server loads the standard library again, which takes several seconds
// in WebAssembly and longer on a loaded CI runner.
const RECOVERY_TIMEOUT_MS = 30_000;

// replaceSource replaces the open file's text with lines.
async function replaceSource(page: Page, lines: string[]) {
    const inputPanel = page.locator('#input-panel');
    await inputPanel.locator('.view-lines').click();
    await page.keyboard.press(`${modifier}+A`);
    // insertText adds the text as one edit, so Monaco closes no brackets.
    await page.keyboard.insertText(lines.join('\n'));
}

// errorHoverText hovers over the first error squiggle in the editor and returns the text
// of the hover that opens, which holds the diagnostic's message.
async function errorHoverText(page: Page): Promise<string> {
    const squiggle = page.locator('#input-panel .squiggly-error').first();
    const box = await squiggle.boundingBox();
    if (!box) {
        throw new Error('no error squiggle is visible');
    }
    // Monaco opens a hover on mouse movement, so the pointer leaves the editor first in
    // case an earlier call left it over the same spot.
    await page.mouse.move(0, 0);
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    // Monaco keeps hidden hover widgets in the page beside the one that shows.
    const hover = page.locator('.monaco-hover:visible');
    await expect(hover).toBeVisible({ timeout: 10_000 });
    return (await hover.textContent()) ?? '';
}

test.describe('Crash recovery', () => {
    test.beforeEach(async ({ page }) => {
        await recordAlerts(page);
        await loadPlayground(page);
    });

    test('a check that panics shows a diagnostic, and the next edit is checked', async ({
        page,
    }) => {
        const inputPanel = page.locator('#input-panel');
        await replaceSource(page, [PANIC_MARKER, 'export val x: number = 1']);
        await expect(inputPanel.locator('.squiggly-error')).toHaveCount(1, {
            timeout: RECOVERY_TIMEOUT_MS,
        });
        expect(await errorHoverText(page)).toContain(
            'the Escalier language server crashed while checking file:///lib/index.esc: forced by @escalier-lsp-test-panic',
        );

        // The same server checks the next edit, which finds a type error.
        await replaceSource(page, ['export val x: number = "a"']);
        await expect(async () => {
            expect(await errorHoverText(page)).toContain(
                'cannot constrain "a" <: number',
            );
        }).toPass({ timeout: RECOVERY_TIMEOUT_MS });
        expect((await recordedAlerts(page)).join('')).not.toContain(
            'language server crashed',
        );
    });

    test('a crash restarts the server, and an edit restarts it after a second crash on the same code', async ({
        page,
    }) => {
        const inputPanel = page.locator('#input-panel');
        await replaceSource(page, [CRASH_MARKER, 'export val x: number = 1']);

        // The first server dies on the edit. The second opens the same text and dies
        // too, so the client waits for an edit.
        await expect(async () => {
            const alerts = (await recordedAlerts(page)).join('\n');
            expect(alerts).toContain(
                'The language server crashed and was restarted.',
            );
            expect(alerts).toContain(
                'The language server crashed again on the same code. It will restart when you next edit a file.',
            );
        }).toPass({ timeout: RECOVERY_TIMEOUT_MS });

        // The edit starts a server, whose diagnostics show up in the editor.
        await replaceSource(page, ['export val x: number = "a"']);
        await expect(inputPanel.locator('.squiggly-error')).toHaveCount(1, {
            timeout: RECOVERY_TIMEOUT_MS,
        });
        expect(await errorHoverText(page)).toContain(
            'cannot constrain "a" <: number',
        );
    });
});
