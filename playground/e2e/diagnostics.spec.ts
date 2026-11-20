import { expect, test } from '@playwright/test';

import { COMPILE_TIMEOUT_MS, loadPlayground } from './helpers';

// The modifier Monaco's shortcuts use on this platform.
const modifier = process.platform === 'darwin' ? 'Meta' : 'Control';

test('the status bar counts diagnostics in closed files and lists them in a tab', async ({
    page,
}) => {
    await loadPlayground(page);
    const counts = page.locator('footer').getByRole('button');
    await expect(counts).toHaveAccessibleName('0 errors, 0 warnings', {
        timeout: COMPILE_TIMEOUT_MS,
    });

    const inputPanel = page.locator('#input-panel');
    await inputPanel.locator('.view-lines').click();
    // An added line leaves the rest of the file, and the files that import it, intact.
    await page.keyboard.press(`${modifier}+End`);
    await page.keyboard.insertText('\nexport val bad: number = "a"');
    await expect(counts).toHaveAccessibleName('1 error, 0 warnings', {
        timeout: COMPILE_TIMEOUT_MS,
    });

    // The count covers every file the language server checked, open or not.
    // The close button shows while the pointer is over its tab.
    await page.getByRole('tab', { name: /index\.esc/ }).hover();
    await page.getByLabel('Close index.esc').click();
    await expect(counts).toHaveAccessibleName('1 error, 0 warnings');

    await counts.click();
    await expect(
        page.getByRole('tab', { name: /^Diagnostics/ }),
    ).toHaveAttribute('aria-selected', 'true');
    // Monaco draws spaces as non-breaking spaces, which \s matches.
    await expect(inputPanel.locator('.view-lines')).toContainText(
        /\/lib\/index\.esc:\d+:26:\serror:\scannot\sconstrain\s"a"\s<:\snumber/,
    );
});

test('a permalink leaves the Diagnostics tab out and the reopened page counts afresh', async ({
    page,
    context,
}) => {
    await loadPlayground(page);
    const inputPanel = page.locator('#input-panel');
    await inputPanel.locator('.view-lines').click();
    await page.keyboard.press(`${modifier}+End`);
    await page.keyboard.insertText('\nexport val bad: number = "a"');
    const counts = page.locator('footer').getByRole('button');
    await expect(counts).toHaveAccessibleName('1 error, 0 warnings', {
        timeout: COMPILE_TIMEOUT_MS,
    });
    await counts.click();

    // Share puts the link in the address bar whether or not the clipboard takes it.
    await page.getByRole('button', { name: 'Share' }).click();
    await expect(page).toHaveURL(/#project=[A-Za-z0-9_-]+$/);

    const reopened = await context.newPage();
    await reopened.goto(page.url());
    const reopenedTabs = reopened.getByTestId('input-tablist').getByRole('tab');
    await expect(reopenedTabs).toHaveText([/^index\.esc/]);
    await expect(
        reopened.locator('footer').getByRole('button'),
    ).toHaveAccessibleName('1 error, 0 warnings', {
        timeout: COMPILE_TIMEOUT_MS,
    });
});
