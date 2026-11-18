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

import { Toast } from './toast';

afterEach(() => {
    cleanup();
    vi.useRealTimers();
});

const crashToast = {
    message: 'The language server crashed and was restarted.',
    type: 'warning' as const,
    crashReport: { headline: 'panic: boom', text: 'panic: boom\n' },
};

test('a crash toast offers its report and stays until dismissed', () => {
    vi.useFakeTimers();
    const onDismiss = vi.fn();
    const onShowCrashReport = vi.fn();
    render(
        <Toast
            notification={crashToast}
            onDismiss={onDismiss}
            onShowCrashReport={onShowCrashReport}
        />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Details' }));
    expect(onShowCrashReport).toHaveBeenCalledOnce();

    act(() => {
        vi.advanceTimersByTime(60_000);
    });
    expect(onDismiss).not.toHaveBeenCalled();
});

// Without a way to open the report, the toast has no reason to stay.
test('a crash toast with no report handler dismisses itself', () => {
    vi.useFakeTimers();
    const onDismiss = vi.fn();
    render(<Toast notification={crashToast} onDismiss={onDismiss} />);

    expect(screen.queryByRole('button', { name: 'Details' })).toBeNull();
    act(() => {
        vi.advanceTimersByTime(4000);
    });
    expect(onDismiss).toHaveBeenCalledOnce();
});

test('any other toast offers no details and dismisses itself', () => {
    vi.useFakeTimers();
    const onDismiss = vi.fn();
    render(
        <Toast
            notification={{ message: 'Saved.', type: 'info' }}
            onDismiss={onDismiss}
            onShowCrashReport={() => {}}
        />,
    );

    expect(screen.queryByRole('button', { name: 'Details' })).toBeNull();
    act(() => {
        vi.advanceTimersByTime(4000);
    });
    expect(onDismiss).toHaveBeenCalledOnce();
});
