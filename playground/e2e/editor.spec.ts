import { expect, test } from '@playwright/test';

import { loadPlayground } from './helpers';

test.describe('Editor', () => {
  test.beforeEach(async ({ page }) => {
    await loadPlayground(page);
  });

  test('left editor shows source code', async ({ page }) => {
    // The input panel should contain Escalier source from hello-world's index.esc
    const inputPanel = page.locator('#input-panel');
    // hello-world's index.esc contains a greet function
    await expect(inputPanel.locator('.view-lines')).toContainText('greet');
  });

  test('right editor shows compiled output', async ({ page }) => {
    // Open a build output file to populate the right pane.
    // Expand the build directory, then navigate to the output file.
    await page.getByRole('button', { name: /^▸ build$/ }).click();
    await page.getByRole('button', { name: /^▸ lib$|^▾ lib$/ }).nth(1).click();
    await page.getByRole('button', { name: 'index.js', exact: true }).click();

    // The output panel should now show compiled JavaScript
    const outputPanel = page.locator('#output-panel');
    await expect(outputPanel.locator('.view-lines')).toContainText('function');
  });

  test('editing source triggers recompilation', async ({ page }) => {
    // Click into the left editor and add a new exported function
    const inputPanel = page.locator('#input-panel');
    await inputPanel.click();
    await page.keyboard.press('End');
    await page.keyboard.press('Enter');
    await page.keyboard.press('Enter');
    await page.keyboard.type('export fn added() -> string { return "hi" }');

    // Open the build output to verify recompilation produced new content.
    // Expand build/lib/ and open index.js.
    await page.getByRole('button', { name: /^▸ build$/ }).click();
    await page.getByRole('button', { name: /^▸ lib$|^▾ lib$/ }).nth(1).click();

    // Poll for the `added` function to appear in the compiled output
    const outputPanel = page.locator('#output-panel');
    await page.getByRole('button', { name: 'index.js', exact: true }).click();

    await expect(async () => {
      const content = await outputPanel.locator('.view-lines').textContent();
      expect(content).toContain('added');
    }).toPass({ timeout: 10_000 });
  });
});
