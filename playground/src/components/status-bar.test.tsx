// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';

import { StatusBar } from './status-bar';

afterEach(cleanup);

test('shows the error count and asks for the list when clicked', () => {
    const onShowErrors = vi.fn();
    render(<StatusBar errorCount={3} onShowErrors={onShowErrors} />);

    fireEvent.click(screen.getByRole('button', { name: '3 errors' }));

    expect(onShowErrors).toHaveBeenCalledOnce();
});

test('says when there are no errors', () => {
    render(<StatusBar errorCount={0} onShowErrors={() => {}} />);

    expect(
        screen.getByRole('button', { name: 'No errors' }),
    ).toBeInTheDocument();
});
