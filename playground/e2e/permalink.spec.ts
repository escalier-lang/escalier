import { expect, test } from '@playwright/test';

import { loadPlayground, waitForCompilation } from './helpers';

// The modifier Monaco's shortcuts use on this platform.
const modifier = process.platform === 'darwin' ? 'Meta' : 'Control';

test('a permalink reopens the project with its edits and tabs', async ({
    browser,
}) => {
    const context = await browser.newContext({
        permissions: ['clipboard-read', 'clipboard-write'],
    });
    const page = await context.newPage();
    await loadPlayground(page);

    // Edit lib/index.esc without saving. The link takes the editor's text.
    const inputPanel = page.locator('#input-panel');
    await inputPanel.locator('.view-lines').click();
    await page.keyboard.press(`${modifier}+A`);
    await page.keyboard.insertText('export val shared = 42');

    // Move bin/main.esc to the right, where it becomes the visible tab beside the
    // build output, and leave package.json visible on the left.
    const inputTabs = page.getByTestId('input-tablist').getByRole('tab');
    const outputTabs = page.getByTestId('output-tablist').getByRole('tab');
    await page.getByRole('button', { name: 'main.esc' }).click();
    await inputTabs.filter({ hasText: 'main.esc' }).click({ button: 'right' });
    await page.getByRole('button', { name: 'Move to Right' }).click();
    // The move leaves the right side focused, and the explorer opens files on the
    // focused side.
    await inputTabs.first().click();
    await page.getByRole('button', { name: 'package.json' }).click();
    const leftNames = await inputTabs.allTextContents();
    const rightNames = await outputTabs.allTextContents();
    expect(leftNames).toHaveLength(2);
    expect(rightNames.at(-1)).toContain('main.esc');

    await page.getByRole('button', { name: 'Share' }).click();
    await expect(
        page.getByRole('alert').filter({
            hasText: 'Copied a link to this project.',
        }),
    ).toBeVisible();
    const link = await page.evaluate(() => navigator.clipboard.readText());
    expect(link).toBe(page.url());
    expect(link).toMatch(/#project=[A-Za-z0-9_-]+$/);

    // A fresh page opens the link.
    const reopened = await context.newPage();
    await reopened.goto(link, { waitUntil: 'networkidle' });
    await waitForCompilation(reopened);

    const reopenedInputTabs = reopened
        .getByTestId('input-tablist')
        .getByRole('tab');
    const reopenedOutputTabs = reopened
        .getByTestId('output-tablist')
        .getByRole('tab');
    await expect(reopenedInputTabs).toHaveText(leftNames);
    await expect(reopenedOutputTabs).toHaveText(rightNames);
    await expect(reopenedInputTabs.nth(1)).toHaveAttribute(
        'aria-selected',
        'true',
    );
    await expect(reopenedOutputTabs.last()).toHaveAttribute(
        'aria-selected',
        'true',
    );

    await reopenedInputTabs.first().click();
    await expect(reopened.locator('#input-panel .view-lines')).toContainText(
        'export val shared = 42',
    );

    await context.close();
});

test('a damaged permalink says so and loads an example', async ({ page }) => {
    await page.goto('/#project=damaged', { waitUntil: 'networkidle' });

    await expect(
        page.getByRole('alert').filter({
            hasText:
                'Could not open the project in this link. The link is damaged or incomplete. Loading an example instead.',
        }),
    ).toBeVisible({ timeout: 30_000 });
    await waitForCompilation(page);
});
