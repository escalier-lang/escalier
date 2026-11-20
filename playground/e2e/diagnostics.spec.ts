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
    // Closing the last tab on the left moves focus to the right. Clicking the
    // empty left pane makes the Diagnostics tab open on the left.
    await page
        .getByRole('button', { name: /^Click to open files here/ })
        .click();
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

test('a permalink reopens the Diagnostics tab, filled from a fresh check', async ({
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
    // With the source tab closed, the Diagnostics tab is the only tab on the left.
    await page.getByRole('tab', { name: /index\.esc/ }).hover();
    await page.getByLabel('Close index.esc').click();

    // Share puts the link in the address bar whether or not the clipboard takes it.
    await page.getByRole('button', { name: 'Share' }).click();
    await expect(page).toHaveURL(/#project=[A-Za-z0-9_-]+$/);

    const reopened = await context.newPage();
    await reopened.goto(page.url());
    // The tabs show once the reopened page has started the language server, which
    // can take as long as a first compile on a loaded runner.
    await expect(
        reopened.getByRole('tab', { name: /^Diagnostics/ }),
    ).toHaveAttribute('aria-selected', 'true', { timeout: COMPILE_TIMEOUT_MS });
    // The link holds the edited source but none of the diagnostics. The reopened page
    // checks the project and lists the error again.
    await expect(
        reopened.locator('footer').getByRole('button'),
    ).toHaveAccessibleName('1 error, 0 warnings', {
        timeout: COMPILE_TIMEOUT_MS,
    });
    await expect(reopened.locator('#input-panel .view-lines')).toContainText(
        /\/lib\/index\.esc:\d+:26:\serror:\scannot\sconstrain/,
    );
});

test('clicking a diagnostic opens its file beside the Diagnostics tab with the cursor at the diagnostic', async ({
    page,
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
    await page.getByRole('tab', { name: /index\.esc/ }).hover();
    await page.getByLabel('Close index.esc').click();
    // Closing the last tab on the left moves focus to the right. Clicking the
    // empty left pane makes the Diagnostics tab open on the left.
    await page
        .getByRole('button', { name: /^Click to open files here/ })
        .click();
    await counts.click();

    await inputPanel
        .locator('.view-line')
        .filter({ hasText: /cannot\sconstrain/ })
        .click();

    // The file opens in the right pane, and the Diagnostics tab stays open on the left.
    await expect(
        page
            .getByTestId('output-tablist')
            .getByRole('tab', { name: /^index\.esc/ }),
    ).toHaveAttribute('aria-selected', 'true');
    await expect(
        page
            .getByTestId('input-tablist')
            .getByRole('tab', { name: /^Diagnostics/ }),
    ).toHaveAttribute('aria-selected', 'true');
    // The cursor sits where the diagnostic starts, at the string literal, so typed
    // text lands just before it.
    await page.keyboard.insertText('X');
    await expect(page.locator('#output-panel .view-lines')).toContainText(
        /export\sval\sbad:\snumber\s=\sX"a"/,
    );
});
