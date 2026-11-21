// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { useRef } from 'react';
import { afterEach, beforeAll, expect, test, vi } from 'vitest';

import { stubModalDialogs } from './dialog-stub';
import { useModalDialog } from './use-modal-dialog';

beforeAll(stubModalDialogs);

afterEach(() => {
    cleanup();
});

/** A dialog that uses the hook, with a button the hook focuses. */
const TestDialog = ({ onCancel }: { onCancel: () => void }) => {
    const buttonRef = useRef<HTMLButtonElement>(null);
    const { dialogRef, onBackdropClick } = useModalDialog(onCancel, buttonRef);
    return (
        // oxlint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-noninteractive-element-interactions -- the dialog's cancel event handles keys
        <dialog ref={dialogRef} aria-label="Test" onClick={onBackdropClick}>
            <button type="button" ref={buttonRef}>
                Inside
            </button>
        </dialog>
    );
};

test('opens the dialog and focuses the given element', () => {
    render(<TestDialog onCancel={() => {}} />);

    expect(screen.getByRole('dialog', { name: 'Test' })).toHaveAttribute(
        'open',
    );
    expect(screen.getByRole('button', { name: 'Inside' })).toHaveFocus();
});

test('calls onCancel on Escape instead of closing on its own', () => {
    const onCancel = vi.fn();
    render(<TestDialog onCancel={onCancel} />);
    const dialog = screen.getByRole('dialog', { name: 'Test' });

    // A browser fires `cancel` on the dialog when the user presses Escape.
    const cancel = new Event('cancel', { cancelable: true });
    dialog.dispatchEvent(cancel);

    expect(onCancel).toHaveBeenCalledOnce();
    expect(cancel.defaultPrevented).toBe(true);
});

test('calls onCancel on a backdrop click but not on a click inside', () => {
    const onCancel = vi.fn();
    render(<TestDialog onCancel={onCancel} />);

    fireEvent.click(screen.getByRole('button', { name: 'Inside' }));
    expect(onCancel).not.toHaveBeenCalled();

    // A click on the backdrop lands on the dialog element itself.
    fireEvent.click(screen.getByRole('dialog', { name: 'Test' }));
    expect(onCancel).toHaveBeenCalledOnce();
});

test('closes the dialog when the component unmounts', () => {
    const { unmount } = render(<TestDialog onCancel={() => {}} />);
    const dialog = screen.getByRole('dialog', { name: 'Test' });

    unmount();

    expect(dialog).not.toHaveAttribute('open');
});
