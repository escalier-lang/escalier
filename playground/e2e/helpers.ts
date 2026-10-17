import { type Page, expect } from '@playwright/test';

/**
 * How long a test waits for the playground's first compile. That compile loads the
 * standard library in WebAssembly on the page's main thread, which takes several
 * seconds and longer on a loaded CI runner.
 */
export const COMPILE_TIMEOUT_MS = 30_000;

/**
 * Wait for the WASM LSP to initialize and compilation to complete.
 *
 * Verifies that the `/build` directory appears in the file explorer and
 * contains compiled output files. Expands `build/lib/` and checks for the
 * expected `.js` files. By default checks for `index.js` (from `lib/index.esc`
 * which all projects have). Pass `files` to override with project-specific
 * build artifacts.
 */
export async function waitForCompilation(
    page: Page,
    { files = ['index.js'] }: { files?: string[] } = {},
): Promise<void> {
    const buildButton = page.getByRole('button', { name: /^[▸▾] build$/ });
    await expect(buildButton).toBeVisible({ timeout: COMPILE_TIMEOUT_MS });

    // Expand build/ then build/lib/ to access compiled output files.
    // The second lib button (nth(1)) is the one inside build/, since the
    // source lib/ directory also exists in the explorer.
    await buildButton.click();
    const buildLibButton = page
        .getByRole('button', { name: /^[▸▾] lib$/ })
        .nth(1);
    await buildLibButton.click();

    for (const file of files) {
        await expect(
            page.getByRole('button', { name: file, exact: true }),
        ).toBeVisible({ timeout: 5_000 });
    }

    // Collapse to restore initial explorer state
    await buildLibButton.click();
    await buildButton.click();
}

/**
 * Record the text of every alert the page shows, from before the page loads. A toast
 * dismisses itself after a few seconds, and the first compile can hold the main thread
 * for longer than that, so a test reads the record rather than polling the DOM.
 */
export async function recordAlerts(page: Page): Promise<void> {
    await page.addInitScript(() => {
        const alerts: string[] = [];
        (window as unknown as { recordedAlerts: string[] }).recordedAlerts =
            alerts;
        new MutationObserver(() => {
            for (const el of document.querySelectorAll('[role="alert"]')) {
                const text = el.textContent ?? '';
                if (!alerts.includes(text)) {
                    alerts.push(text);
                }
            }
        }).observe(document, { childList: true, subtree: true });
    });
}

/**
 * Return the text of every alert recordAlerts has seen so far.
 */
export async function recordedAlerts(page: Page): Promise<string[]> {
    return page.evaluate(
        () =>
            (window as unknown as { recordedAlerts: string[] }).recordedAlerts,
    );
}

/**
 * Navigate to the playground and wait for it to be ready.
 */
export async function loadPlayground(
    page: Page,
    params?: Record<string, string>,
): Promise<void> {
    const url = params ? `/?${new URLSearchParams(params).toString()}` : '/';
    await page.goto(url, { waitUntil: 'networkidle' });
    await waitForCompilation(page);
}
