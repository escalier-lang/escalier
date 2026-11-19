import { expect, test } from '@playwright/test';

import { COMPILE_TIMEOUT_MS, loadPlayground } from './helpers';

// The modifier Monaco's shortcuts use on this platform.
const modifier = process.platform === 'darwin' ? 'Meta' : 'Control';

test('Report a bug fills in an issue with the errors, a project link and the build', async ({
    page,
}) => {
    await loadPlayground(page);

    // A type error gives the report an error to include.
    const inputPanel = page.locator('#input-panel');
    await inputPanel.locator('.view-lines').click();
    await page.keyboard.press(`${modifier}+A`);
    await page.keyboard.insertText('export val x: number = "a"');
    await expect(inputPanel.locator('.squiggly-error')).toHaveCount(1, {
        timeout: COMPILE_TIMEOUT_MS,
    });

    await page.getByRole('button', { name: 'Report a bug' }).click();
    const dialog = page.getByRole('dialog', { name: 'Report a bug' });
    await dialog.getByLabel('What happened').fill('The error is confusing.');

    const issueLink = dialog.getByRole('link', { name: 'Open GitHub issue' });
    const href = (await issueLink.getAttribute('href')) ?? '';
    const url = new URL(href);
    expect(`${url.origin}${url.pathname}`).toBe(
        'https://github.com/escalier-lang/escalier/issues/new',
    );
    const fields = url.searchParams;
    expect(fields.get('template')).toBe('playground-bug.yml');
    expect(fields.get('title')).toBe('Playground: The error is confusing.');
    expect(fields.get('errors')).toContain(
        '/lib/index.esc:1:24: cannot constrain "a" <: number',
    );
    expect(fields.get('playground-link')).toMatch(
        /^http:\/\/localhost:5173\/#project=[A-Za-z0-9_-]+$/,
    );
    expect(fields.get('environment')).toMatch(
        /^Browser: .+\nPlayground build: [0-9a-f]{40}$/,
    );

    await dialog.getByRole('button', { name: 'Cancel' }).click();
    await expect(dialog).toBeHidden();
});
