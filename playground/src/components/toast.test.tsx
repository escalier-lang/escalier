// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest';
import {
    act,
    cleanup,
    fireEvent,
    render,
    screen,
} from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';

import { Toast, ToastButton } from './toast';

afterEach(() => {
    cleanup();
    vi.useRealTimers();
});

test('shows its children and dismisses itself after four seconds', () => {
    vi.useFakeTimers();
    const onDismiss = vi.fn();
    render(
        <Toast type="info" onDismiss={onDismiss}>
            <span>Saved.</span>
        </Toast>,
    );

    expect(screen.getByRole('alert')).toHaveTextContent('Saved.');
    act(() => {
        vi.advanceTimersByTime(3999);
    });
    expect(onDismiss).not.toHaveBeenCalled();
    act(() => {
        vi.advanceTimersByTime(1);
    });
    expect(onDismiss).toHaveBeenCalledOnce();
});

test('a persistent toast stays until dismissed', () => {
    vi.useFakeTimers();
    const onDismiss = vi.fn();
    render(
        <Toast type="warning" persistent onDismiss={onDismiss}>
            <span>The language server crashed and was restarted.</span>
        </Toast>,
    );

    act(() => {
        vi.advanceTimersByTime(60_000);
    });
    expect(onDismiss).not.toHaveBeenCalled();

    fireEvent.click(
        screen.getByRole('button', { name: 'Dismiss notification' }),
    );
    expect(onDismiss).toHaveBeenCalledOnce();
});

test('a ToastButton calls its handler when clicked', () => {
    const onClick = vi.fn();
    render(
        <Toast type="warning" persistent onDismiss={() => {}}>
            <ToastButton onClick={onClick}>Details</ToastButton>
        </Toast>,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Details' }));
    expect(onClick).toHaveBeenCalledOnce();
});
