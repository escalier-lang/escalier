// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeAll, expect, test, vi } from 'vitest';

import { stubModalDialogs } from './dialog-stub';
import { MessageDialog } from './message-dialog';

beforeAll(stubModalDialogs);

afterEach(() => {
    cleanup();
});

test('shows the title and message, and OK closes it', () => {
    const onClose = vi.fn();
    render(
        <MessageDialog
            title="Project too large to share"
            message="Remove some files."
            onClose={onClose}
        />,
    );

    const dialog = screen.getByRole('dialog', {
        name: 'Project too large to share',
    });
    expect(dialog).toHaveAttribute('open');
    expect(dialog).toHaveAccessibleDescription('Remove some files.');
    const ok = screen.getByRole('button', { name: 'OK' });
    expect(ok).toHaveFocus();

    fireEvent.click(ok);
    expect(onClose).toHaveBeenCalledOnce();
});
