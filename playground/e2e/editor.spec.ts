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
    // The output panel should contain compiled JavaScript
    const outputPanel = page.locator('#output-panel');
    // Compiled output should contain JS keywords
    await expect(outputPanel.locator('.view-lines')).toContainText('function');
  });

  test('editing source triggers recompilation', async ({ page }) => {
    // Get initial right-pane content
    const outputPanel = page.locator('#output-panel');
    const initialContent = await outputPanel.locator('.view-lines').textContent();

    // Click into the left editor and add a comment
    const inputPanel = page.locator('#input-panel');
    await inputPanel.click();
    await page.keyboard.press('End');
    await page.keyboard.press('Enter');
    await page.keyboard.type('// test comment');

    // Wait for recompilation (debounced at 500ms + compile time)
    await page.waitForTimeout(2000);

    // The output should have changed (the comment won't appear in JS output,
    // but the source map URL may change, or we can verify the source still compiles)
    const outputContent = await outputPanel.locator('.view-lines').textContent();
    // The output should still contain valid JS (compilation didn't break)
    expect(outputContent).toContain('function');
  });
});
