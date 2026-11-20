// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';

import { StatusBar } from './status-bar';

afterEach(cleanup);

test('shows the error and warning counts and asks for the list when clicked', () => {
    const onShowDiagnostics = vi.fn();
    render(
        <StatusBar
            counts={{ errors: 3, warnings: 1 }}
            onShowDiagnostics={onShowDiagnostics}
        />,
    );

    const button = screen.getByRole('button', {
        name: '3 errors, 1 warning',
    });
    expect(button).toHaveTextContent('31');
    fireEvent.click(button);

    expect(onShowDiagnostics).toHaveBeenCalledOnce();
});

test('names the counts when there are none', () => {
    render(
        <StatusBar
            counts={{ errors: 0, warnings: 0 }}
            onShowDiagnostics={() => {}}
        />,
    );

    expect(
        screen.getByRole('button', { name: '0 errors, 0 warnings' }),
    ).toBeInTheDocument();
});
