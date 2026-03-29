import type { Meta, StoryObj } from '@storybook/react-vite';
import type { Dispatch } from 'react';
import { expect, fn, userEvent, waitFor, within } from 'storybook/test';

import type { Notification, PlaygroundAction, PlaygroundState } from '../state';
import {
    PlaygroundDispatchContext,
    PlaygroundStateContext,
    initialState,
} from '../state';

import { Toast } from './toast';

const dispatchSpy = fn();

function ToastWithProvider({ notification }: { notification: Notification }) {
    const state: PlaygroundState = { ...initialState, notification };
    const dispatch: Dispatch<PlaygroundAction> = (action) => {
        dispatchSpy(action);
    };

    return (
        <PlaygroundStateContext.Provider value={state}>
            <PlaygroundDispatchContext.Provider value={dispatch}>
                <Toast />
            </PlaygroundDispatchContext.Provider>
        </PlaygroundStateContext.Provider>
    );
}

const meta = {
    title: 'Components/Toast',
    component: ToastWithProvider,
    beforeEach: () => {
        dispatchSpy.mockClear();
    },
} satisfies Meta<typeof ToastWithProvider>;

export default meta;
type Story = StoryObj<typeof meta>;

export const InfoToast: Story = {
    args: {
        notification: {
            message: 'File saved successfully.',
            type: 'info',
        },
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
        notification: {
            message: 'Workspace validation found issues.',
            type: 'warning',
        },
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
        notification: {
            message: 'Compilation failed with 3 errors.',
            type: 'error',
        },
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
        notification: {
            message: 'Click dismiss to close.',
            type: 'info',
        },
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

        // Verify dispatch was called with dismissNotification
        await expect(dispatchSpy).toHaveBeenCalledWith({
            type: 'dismissNotification',
        });
    },
};

export const LongMessage: Story = {
    args: {
        notification: {
            message:
                'This is a much longer notification message to see how the toast handles text that might wrap to multiple lines in the UI.',
            type: 'info',
        },
    },
};
