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

// openCrashReport opens the crash report from the toast, checks that it holds each of
// expected, and closes it. It returns the link the report's issue button opens.
async function openCrashReport(
    page: Page,
    expected: string[],
): Promise<string> {
    await page
        .getByRole('alert')
        .getByRole('button', { name: 'Details' })
        .click();
    const dialog = page.getByRole('dialog', {
        name: 'Language server crash report',
    });
    await expect(dialog).toBeVisible();
    const report = dialog.getByLabel('Crash report');
    for (const text of expected) {
        await expect(report).toContainText(text);
    }
    const href =
        (await dialog
            .getByRole('link', { name: 'Open GitHub issue' })
            .getAttribute('href')) ?? '';
    await dialog.getByRole('button', { name: 'Close' }).click();
    await expect(dialog).toBeHidden();
    return href;
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
        // A toast says the server recovered, and its report holds the panic and stack.
        await expect(
            page.getByRole('alert').filter({
                hasText:
                    'The language server hit an internal error and recovered.',
            }),
        ).toBeVisible();
        await openCrashReport(page, [
            'the Escalier language server crashed while checking file:///lib/index.esc: forced by @escalier-lsp-test-panic',
            '(*Server).runTestHooks(',
            'Playground link: http://localhost:5173/#project=',
        ]);

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

        // The report holds what the Go runtime printed as the panic ended the server, and
        // the issue link carries it too.
        const issueUrl = await openCrashReport(page, [
            'The language server crashed again on the same code.',
            'panic: forced by @escalier-lsp-test-crash',
            'goroutine',
            'Playground link: http://localhost:5173/#project=',
        ]);
        expect(issueUrl).toMatch(
            /^https:\/\/github\.com\/escalier-lang\/escalier\/issues\/new\?/,
        );
        const fields = new URL(issueUrl).searchParams;
        expect(fields.get('template')).toBe('playground-bug.yml');
        expect(fields.get('crash-report')).toContain(
            'panic: forced by @escalier-lsp-test-crash',
        );
        expect(fields.get('playground-link')).toMatch(
            /^http:\/\/localhost:5173\/#project=[A-Za-z0-9_-]+$/,
        );
        expect(fields.get('environment')).toMatch(
            /^Browser: .+\nPlayground build: [0-9a-f]{40}$/,
        );

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
