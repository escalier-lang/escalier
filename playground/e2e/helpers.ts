import { type Page, expect } from '@playwright/test';

/**
 * Wait for the WASM LSP to initialize and the first compilation to complete.
 *
 * We detect compilation completion by checking for the `/build` directory in
 * the file explorer. Due to a race condition in the app, the output tabs may
 * not always auto-open (the FS event can fire before the React effect listener
 * is registered), so checking for the build directory is more reliable than
 * waiting for output tabs.
 */
export async function waitForCompilation(page: Page): Promise<void> {
  // Wait for the `build` directory to appear in the file explorer.
  // This is the most reliable signal that compilation has completed.
  await expect(
    page.getByRole('button', { name: /build/ }),
  ).toBeVisible({ timeout: 45_000 });
}

/**
 * Navigate to the playground and wait for it to be ready.
 */
export async function loadPlayground(
  page: Page,
  params?: Record<string, string>,
): Promise<void> {
  const url = params
    ? `/?${new URLSearchParams(params).toString()}`
    : '/';
  await page.goto(url, { waitUntil: 'networkidle' });
  await waitForCompilation(page);
}
