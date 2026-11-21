// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

import { useThemeStore } from '#theme/theme';

import { Toolbar } from './toolbar';

function renderToolbar(
    onShare: () => void = () => {},
    onReportBug: () => void = () => {},
) {
    render(
        <Toolbar
            templates={[]}
            examples={[]}
            onSelectTemplate={() => {}}
            onSelectExample={() => {}}
            onShare={onShare}
            onReportBug={onReportBug}
        />,
    );
}

afterEach(() => {
    cleanup();
});

test('the Report a bug button asks for a bug report', () => {
    const onReportBug = vi.fn();
    renderToolbar(undefined, onReportBug);

    fireEvent.click(screen.getByRole('button', { name: 'Report a bug' }));

    expect(onReportBug).toHaveBeenCalledOnce();
});

test('links to the repository on GitHub in a new tab', () => {
    renderToolbar();

    const link = screen.getByRole('link', { name: 'GitHub' });
    expect(link).toHaveAttribute(
        'href',
        'https://github.com/escalier-lang/escalier',
    );
    expect(link).toHaveAttribute('target', '_blank');
});

test('the Share button asks for a permalink', () => {
    const onShare = vi.fn();
    renderToolbar(onShare);

    fireEvent.click(screen.getByRole('button', { name: 'Share' }));

    expect(onShare).toHaveBeenCalledOnce();
});

describe('Toolbar theme menu', () => {
    beforeEach(() => {
        useThemeStore.setState({
            preference: 'system',
            systemPrefersLight: false,
            theme: 'dark',
        });
    });

    test('checks the current preference', () => {
        renderToolbar();

        fireEvent.click(screen.getByRole('button', { name: /Theme: System/ }));

        expect(
            screen.getByRole('menuitemradio', { name: 'System' }),
        ).toHaveAttribute('aria-checked', 'true');
        expect(
            screen.getByRole('menuitemradio', { name: 'Light' }),
        ).toHaveAttribute('aria-checked', 'false');
    });

    test('sets the preference the user picks', () => {
        renderToolbar();

        fireEvent.click(screen.getByRole('button', { name: /Theme: System/ }));
        fireEvent.click(screen.getByRole('menuitemradio', { name: 'Light' }));

        expect(useThemeStore.getState().preference).toBe('light');
        expect(useThemeStore.getState().theme).toBe('light');
        expect(
            screen.getByRole('button', { name: /Theme: Light/ }),
        ).toBeInTheDocument();
    });
});
