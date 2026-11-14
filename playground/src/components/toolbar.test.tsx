// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, test } from 'vitest';

import { useThemeStore } from '../theme';
import { Toolbar } from './toolbar';

function renderToolbar() {
    render(
        <Toolbar
            templates={[]}
            examples={[]}
            onSelectTemplate={() => {}}
            onSelectExample={() => {}}
        />,
    );
}

describe('Toolbar theme menu', () => {
    beforeEach(() => {
        useThemeStore.setState({
            preference: 'system',
            systemPrefersLight: false,
            theme: 'dark',
        });
    });

    afterEach(() => {
        cleanup();
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
