import { type Locator, type Page, expect } from '@playwright/test';

/**
 * Wait for the WASM LSP to initialize and compilation to complete.
 *
 * We detect compilation completion by checking for the `/build` directory in
 * the file explorer. Due to a race condition in the app, the output tabs may
 * not always auto-open (the FS event can fire before the React effect listener
 * is registered), so checking for the build directory is more reliable than
 * waiting for output tabs.
 *
 * When called after a project switch, pass a `verify` locator that is unique
 * to the new project (e.g. a file that only exists in the new project) so the
 * check doesn't pass on stale build artifacts from the previous project.
 */
export async function waitForCompilation(
    page: Page,
    { verify }: { verify?: Locator } = {},
): Promise<void> {
    await expect(page.getByRole('button', { name: /build/ })).toBeVisible({
        timeout: 10_000,
    });
    if (verify) {
        await expect(verify).toBeVisible({ timeout: 5_000 });
    }
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
