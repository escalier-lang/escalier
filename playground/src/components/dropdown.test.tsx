// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';

import { Dropdown } from './dropdown';

afterEach(cleanup);

/** Renders a dropdown beside a button outside it, and opens its menu. */
function renderOpenDropdown() {
    render(
        <>
            <Dropdown
                label="Examples"
                items={[{ slug: 'enums', label: 'Enums' }]}
                onSelect={vi.fn()}
            />
            <button type="button">Outside</button>
        </>,
    );
    fireEvent.click(screen.getByRole('button', { name: /Examples/ }));
    expect(screen.getByRole('menuitem', { name: 'Enums' })).toBeInTheDocument();
}

test('focus leaving the dropdown closes its menu', () => {
    renderOpenDropdown();

    fireEvent.blur(screen.getByRole('button', { name: /Examples/ }), {
        relatedTarget: screen.getByRole('button', { name: 'Outside' }),
    });

    expect(
        screen.queryByRole('menuitem', { name: 'Enums' }),
    ).not.toBeInTheDocument();
});

test('focus moving into the menu keeps it open', () => {
    renderOpenDropdown();

    fireEvent.blur(screen.getByRole('button', { name: /Examples/ }), {
        relatedTarget: screen.getByRole('menuitem', { name: 'Enums' }),
    });

    expect(screen.getByRole('menuitem', { name: 'Enums' })).toBeInTheDocument();
});
