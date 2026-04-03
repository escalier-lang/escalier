import { type Page, expect } from '@playwright/test';

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
    await expect(buildButton).toBeVisible({ timeout: 10_000 });

    // Expand build/ then build/lib/ to access compiled output files.
    // The second lib button (nth(1)) is the one inside build/, since the
    // source lib/ directory also exists in the explorer.
    await buildButton.click();
    const buildLibButton = page.getByRole('button', { name: /^[▸▾] lib$/ }).nth(1);
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
