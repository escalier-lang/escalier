import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, fn, userEvent, within } from 'storybook/test';

import { ConfirmDialog } from './confirm-dialog';

const meta = {
    title: 'Components/ConfirmDialog',
    component: ConfirmDialog,
    args: {
        onConfirm: fn(),
        onCancel: fn(),
    },
} satisfies Meta<typeof ConfirmDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    args: {
        title: 'Confirm action',
        message: 'Are you sure you want to proceed?',
    },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement.ownerDocument.body);

        // Dialog renders with title and message
        await expect(canvas.getByText('Confirm action')).toBeVisible();
        await expect(
            canvas.getByText('Are you sure you want to proceed?'),
        ).toBeVisible();

        // Default button labels
        await expect(
            canvas.getByRole('button', { name: 'Cancel' }),
        ).toBeVisible();
        await expect(
            canvas.getByRole('button', { name: 'Confirm' }),
        ).toBeVisible();

        // Cancel button is focused by default
        await expect(
            canvas.getByRole('button', { name: 'Cancel' }),
        ).toHaveFocus();

        // Clicking confirm calls onConfirm
        await userEvent.click(canvas.getByRole('button', { name: 'Confirm' }));
        await expect(args.onConfirm).toHaveBeenCalledOnce();
    },
};

export const CustomLabels: Story = {
    args: {
        title: 'Save changes?',
        message:
            'You have unsaved changes. Do you want to save before closing?',
        confirmLabel: 'Save',
        cancelLabel: 'Discard',
    },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement.ownerDocument.body);

        // Custom labels are rendered
        await expect(
            canvas.getByRole('button', { name: 'Discard' }),
        ).toBeVisible();
        await expect(
            canvas.getByRole('button', { name: 'Save' }),
        ).toBeVisible();

        // Clicking cancel calls onCancel
        await userEvent.click(canvas.getByRole('button', { name: 'Discard' }));
        await expect(args.onCancel).toHaveBeenCalledOnce();
    },
};

export const Destructive: Story = {
    args: {
        title: 'Delete file',
        message:
            'This will permanently delete "main.esc". This action cannot be undone.',
        confirmLabel: 'Delete',
        destructive: true,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement.ownerDocument.body);

        // The confirm button should have the destructive styling
        const deleteButton = canvas.getByRole('button', { name: 'Delete' });
        await expect(deleteButton).toBeVisible();

        // The destructive button should have a red-ish background
        const style = getComputedStyle(deleteButton);
        await expect(style.backgroundColor).toBe('rgb(212, 32, 32)');
    },
};

export const AccessibilityAttributes: Story = {
    args: {
        title: 'Accessible dialog',
        message: 'This dialog should have proper ARIA attributes.',
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement.ownerDocument.body);

        const dialog = canvas.getByRole('dialog');
        await expect(dialog).toBeVisible();

        // aria-labelledby points to the title
        await expect(dialog).toHaveAttribute(
            'aria-labelledby',
            'confirm-dialog-title',
        );
        // aria-describedby points to the message
        await expect(dialog).toHaveAttribute(
            'aria-describedby',
            'confirm-dialog-message',
        );
        await expect(dialog).toHaveAttribute('aria-modal', 'true');
    },
};
