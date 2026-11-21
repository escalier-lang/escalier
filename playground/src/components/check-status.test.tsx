// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest';
import { act, cleanup, render, screen } from '@testing-library/react';
import { afterEach, expect, test } from 'vitest';

import { useCheckStore } from '#diagnostics/check-monitor';

import { CheckStatusIndicator } from './check-status';

afterEach(() => {
    cleanup();
    useCheckStore.setState({ status: { kind: 'idle', lastCheckMs: null } });
});

test('the indicator shows the status text', () => {
    render(<CheckStatusIndicator />);
    act(() =>
        useCheckStore.setState({ status: { kind: 'idle', lastCheckMs: 420 } }),
    );

    expect(screen.getByRole('status')).toHaveTextContent('Checked in 0.4 s');
});
