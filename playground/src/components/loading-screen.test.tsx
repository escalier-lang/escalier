// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest';
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, test } from 'vitest';

import {
    LoadingScreen,
    formatMegabytes,
    percentLoaded,
} from './loading-screen';

afterEach(() => {
    cleanup();
});

describe('formatMegabytes', () => {
    test.each([
        [0, '0.0 MB'],
        [2_449_999, '2.4 MB'],
        [24_500_000, '24.5 MB'],
    ])('formats %d bytes as %s', (bytes, expected) => {
        expect(formatMegabytes(bytes)).toBe(expected);
    });
});

describe('percentLoaded', () => {
    test.each([
        [{ loaded: 0, total: 200 }, 0],
        [{ loaded: 99, total: 200 }, 49],
        [{ loaded: 200, total: 200 }, 100],
        [{ loaded: 0, total: 0 }, undefined],
    ])('returns the percentage of %o', (progress, expected) => {
        expect(percentLoaded(progress)).toBe(expected);
    });
});

describe('LoadingScreen', () => {
    test('reports the bytes downloaded so far', () => {
        render(
            <LoadingScreen
                status={{
                    kind: 'downloading',
                    progress: { loaded: 6_000_000, total: 24_000_000 },
                }}
            />,
        );

        expect(screen.getByRole('dialog')).toHaveAccessibleName(
            'Loading the Escalier playground',
        );
        expect(screen.getByRole('progressbar')).toHaveValue(25);
        expect(
            screen.getByText(
                'Downloading the language server and standard library: 6.0 MB of 24.0 MB',
            ),
        ).toBeInTheDocument();
    });

    test('shows an indeterminate bar when no download has a known size', () => {
        render(
            <LoadingScreen
                status={{
                    kind: 'downloading',
                    progress: { loaded: 0, total: 0 },
                }}
            />,
        );

        expect(screen.getByRole('progressbar')).not.toHaveAttribute('value');
        expect(
            screen.getByText(
                'Downloading the language server and standard library…',
            ),
        ).toBeInTheDocument();
    });

    test('reports that the language server is starting', () => {
        render(<LoadingScreen status={{ kind: 'starting' }} />);

        expect(screen.getByRole('progressbar')).toHaveValue(100);
        expect(
            screen.getByText('Starting the language server…'),
        ).toBeInTheDocument();
    });

    test('reports a failure as an alert', () => {
        render(
            <LoadingScreen
                status={{
                    kind: 'failed',
                    message: 'Failed to fetch /lsp-server.wasm: Not Found',
                }}
            />,
        );

        expect(screen.getByRole('alert')).toHaveTextContent(
            'Failed to load the playground: Failed to fetch /lsp-server.wasm: Not Found',
        );
    });
});
