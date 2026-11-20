import { expect, test } from '@playwright/test';

import { COMPILE_TIMEOUT_MS, loadPlayground } from './helpers';

// The modifier Monaco's shortcuts use on this platform.
const modifier = process.platform === 'darwin' ? 'Meta' : 'Control';

test('the status bar counts errors in closed files and lists them in a tab', async ({
    page,
}) => {
    await loadPlayground(page);
    const errorCount = page.locator('footer').getByRole('button');
    await expect(errorCount).toHaveText('No errors', {
        timeout: COMPILE_TIMEOUT_MS,
    });

    const inputPanel = page.locator('#input-panel');
    await inputPanel.locator('.view-lines').click();
    // An added line leaves the rest of the file, and the files that import it, intact.
    await page.keyboard.press(`${modifier}+End`);
    await page.keyboard.insertText('\nexport val bad: number = "a"');
    await expect(errorCount).toHaveText('1 error', {
        timeout: COMPILE_TIMEOUT_MS,
    });

    // The count covers every file the language server checked, open or not.
    // The close button shows while the pointer is over its tab.
    await page.getByRole('tab', { name: /index\.esc/ }).hover();
    await page.getByLabel('Close index.esc').click();
    await expect(errorCount).toHaveText('1 error');

    await errorCount.click();
    await expect(page.getByRole('tab', { name: /^Errors/ })).toHaveAttribute(
        'aria-selected',
        'true',
    );
    // Monaco draws spaces as non-breaking spaces, which \s matches.
    await expect(inputPanel.locator('.view-lines')).toContainText(
        /\/lib\/index\.esc:\d+:26:\scannot\sconstrain\s"a"\s<:\snumber/,
    );
});
