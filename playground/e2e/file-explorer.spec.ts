import { expect, test } from '@playwright/test';

import { loadPlayground } from './helpers';

test.describe('File Explorer', () => {
  test.beforeEach(async ({ page }) => {
    await loadPlayground(page);
  });

  test('click file in explorer opens tab', async ({ page }) => {
    // main.esc is visible in the file explorer under bin/
    await page.getByRole('button', { name: 'main.esc' }).click();

    // A new tab should appear in the left tablist
    const inputTablist = page.getByRole('tablist').first();
    await expect(inputTablist.getByRole('tab', { name: /main\.esc/ })).toBeVisible();
  });

  test('create new file via header button', async ({ page }) => {
    await page.getByLabel('New File').click();

    // An inline input should appear
    const input = page.getByLabel('New file name');
    await expect(input).toBeVisible();

    await input.fill('test.esc');
    await input.press('Enter');

    // File should appear in the explorer
    await expect(page.getByRole('button', { name: 'test.esc' })).toBeVisible();

    // A tab should open for the new file
    const inputTablist = page.getByRole('tablist').first();
    await expect(inputTablist.getByRole('tab', { name: /test\.esc/ })).toBeVisible();
  });

  test('create new folder via header button', async ({ page }) => {
    await page.getByLabel('New Folder').click();

    // An inline input should appear
    const input = page.getByLabel('New dir name');
    await expect(input).toBeVisible();

    await input.fill('src');
    await input.press('Enter');

    // Folder should appear in the explorer as an expandable button
    await expect(page.getByRole('button', { name: /src/ })).toBeVisible();
  });

  test('rename file via context menu', async ({ page }) => {
    // Right-click on main.esc in the explorer
    await page.getByRole('button', { name: 'main.esc' }).click({ button: 'right' });

    // Context menu should appear with Rename option
    await page.getByRole('menuitem', { name: 'Rename' }).click();

    // An inline input should appear with the current name
    const input = page.getByLabel('Rename main.esc');
    await expect(input).toBeVisible();

    await input.fill('entry.esc');
    await input.press('Enter');

    // Old name should be gone, new name should appear
    await expect(page.getByRole('button', { name: 'main.esc' })).toBeHidden();
    await expect(page.getByRole('button', { name: 'entry.esc' })).toBeVisible();
  });

  test('delete file with confirmation', async ({ page }) => {
    // Right-click on main.esc
    await page.getByRole('button', { name: 'main.esc' }).click({ button: 'right' });

    // Click Delete in context menu
    await page.getByRole('menuitem', { name: 'Delete' }).click();

    // Confirmation dialog should appear
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText('Are you sure you want to delete');

    // Confirm deletion
    await dialog.getByRole('button', { name: 'Delete' }).click();

    // File should be removed from the explorer
    await expect(page.getByRole('button', { name: 'main.esc' })).toBeHidden();
  });

  test('cancel delete keeps file', async ({ page }) => {
    // Right-click on main.esc
    await page.getByRole('button', { name: 'main.esc' }).click({ button: 'right' });

    // Click Delete in context menu
    await page.getByRole('menuitem', { name: 'Delete' }).click();

    // Cancel in the dialog
    const dialog = page.getByRole('dialog');
    await dialog.getByRole('button', { name: 'Cancel' }).click();

    // File should still exist
    await expect(page.getByRole('button', { name: 'main.esc' })).toBeVisible();
  });
});
