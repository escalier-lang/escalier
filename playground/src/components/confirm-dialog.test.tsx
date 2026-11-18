// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeAll, expect, test, vi } from 'vitest';

import { ConfirmDialog } from './confirm-dialog';

beforeAll(() => {
    // jsdom has no modal dialogs, so these open and close the element by its attribute.
    HTMLDialogElement.prototype.showModal = function () {
        this.open = true;
    };
    HTMLDialogElement.prototype.close = function () {
        this.open = false;
    };
});

afterEach(() => {
    cleanup();
});

/** Renders a ConfirmDialog and returns its two handlers. */
function renderDialog() {
    const onConfirm = vi.fn();
    const onCancel = vi.fn();
    render(
        <ConfirmDialog
            title="Replace project?"
            message="Your changes will be lost."
            confirmLabel="Replace"
            onConfirm={onConfirm}
            onCancel={onCancel}
        />,
    );
    return { onConfirm, onCancel };
}

test('shows the title and message and focuses Cancel', () => {
    renderDialog();

    const dialog = screen.getByRole('dialog', { name: 'Replace project?' });
    expect(dialog).toHaveAttribute('open');
    expect(dialog).toHaveAccessibleDescription('Your changes will be lost.');
    expect(screen.getByRole('button', { name: 'Cancel' })).toHaveFocus();
});

test('calls onConfirm from the confirm button', () => {
    const { onConfirm, onCancel } = renderDialog();

    fireEvent.click(screen.getByRole('button', { name: 'Replace' }));

    expect(onConfirm).toHaveBeenCalledOnce();
    expect(onCancel).not.toHaveBeenCalled();
});

test('calls onCancel from the cancel button and from a backdrop click', () => {
    const { onConfirm, onCancel } = renderDialog();

    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    fireEvent.click(screen.getByRole('dialog', { name: 'Replace project?' }));

    expect(onCancel).toHaveBeenCalledTimes(2);
    expect(onConfirm).not.toHaveBeenCalled();
});
