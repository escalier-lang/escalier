import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, fn, userEvent, waitFor, within } from 'storybook/test';

import { Toast } from './toast';

const dismissSpy = fn();

const LONG_MESSAGE =
    'This is a much longer notification message to see how the toast handles text that might wrap to multiple lines in the UI.';

const meta = {
    title: 'Components/Toast',
    component: Toast,
    beforeEach: () => {
        dismissSpy.mockClear();
    },
} satisfies Meta<typeof Toast>;

export default meta;
type Story = StoryObj<typeof meta>;

export const InfoToast: Story = {
    args: {
        type: 'info',
        children: <span>File saved successfully.</span>,
        onDismiss: dismissSpy,
    },
    play: async ({ canvasElement }) => {
        // Toast uses position:fixed, so query from document body.
        // Use waitFor since the toast may not be visible immediately.
        const canvas = within(canvasElement.ownerDocument.body);

        // Message is displayed
        await waitFor(() =>
            expect(canvas.getByText('File saved successfully.')).toBeVisible(),
        );

        // Has role="alert" for screen readers
        await waitFor(() => expect(canvas.getByRole('alert')).toBeVisible());

        // Dismiss button is present
        await waitFor(() =>
            expect(
                canvas.getByRole('button', { name: 'Dismiss notification' }),
            ).toBeVisible(),
        );
    },
};

export const WarningToast: Story = {
    args: {
        type: 'warning',
        children: <span>Workspace validation found issues.</span>,
        onDismiss: dismissSpy,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement.ownerDocument.body);
        await waitFor(() =>
            expect(
                canvas.getByText('Workspace validation found issues.'),
            ).toBeVisible(),
        );
    },
};

export const ErrorToast: Story = {
    args: {
        type: 'error',
        children: <span>Compilation failed with 3 errors.</span>,
        onDismiss: dismissSpy,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement.ownerDocument.body);
        await waitFor(() =>
            expect(
                canvas.getByText('Compilation failed with 3 errors.'),
            ).toBeVisible(),
        );
    },
};

export const DismissToast: Story = {
    args: {
        type: 'info',
        children: <span>Click dismiss to close.</span>,
        onDismiss: dismissSpy,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement.ownerDocument.body);

        // Wait for toast to be visible before interacting
        await waitFor(() =>
            expect(
                canvas.getByRole('button', { name: 'Dismiss notification' }),
            ).toBeVisible(),
        );

        // Click the dismiss button
        await userEvent.click(
            canvas.getByRole('button', { name: 'Dismiss notification' }),
        );

        // Verify onDismiss was called
        await expect(dismissSpy).toHaveBeenCalled();
    },
};

export const LongMessage: Story = {
    args: {
        type: 'info',
        children: <span>{LONG_MESSAGE}</span>,
        onDismiss: dismissSpy,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement.ownerDocument.body);

        // Verify the long message renders and is visible
        await waitFor(() =>
            expect(canvas.getByText(LONG_MESSAGE)).toBeVisible(),
        );
    },
};
