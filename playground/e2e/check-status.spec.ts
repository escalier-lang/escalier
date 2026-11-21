import { expect, test } from '@playwright/test';

import { SPIN_MARKER, loadPlayground, replaceSource } from './helpers';

test.describe('Check status', () => {
    test('the status bar shows a slow check while it runs, then how long it took', async ({
        page,
    }) => {
        await loadPlayground(page);
        const status = page.getByRole('status');
        // The first check has finished, so the status bar shows how long it took.
        await expect(status).toHaveText(/^Checked in \d+\.\d s$/);

        await replaceSource(page, [
            `${SPIN_MARKER} 4000`,
            'export val x: number = 1',
        ]);

        await expect(status).toHaveText(/^Checking… \d+\.\d s$/, {
            timeout: 5_000,
        });
        await expect(status).toHaveText(/^Checked in [4-9]\.\d s$/, {
            timeout: 10_000,
        });
    });
});
