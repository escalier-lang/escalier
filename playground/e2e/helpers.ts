import { type Page, expect } from '@playwright/test';

/**
 * Wait for the WASM LSP to initialize and the first compilation to complete.
 * The compilation is done when output tabs appear on the right side.
 */
export async function waitForCompilation(page: Page): Promise<void> {
  // Wait for at least one output tab to appear on the right side.
  // This is the most reliable signal that the WASM LSP has initialized,
  // compiled the project, and the output files have been opened.
  const outputTablist = page.getByRole('tablist').nth(1);
  await expect(outputTablist.getByRole('tab').first()).toBeVisible({ timeout: 45_000 });
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
