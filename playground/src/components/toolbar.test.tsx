// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import {
    afterEach,
    beforeAll,
    beforeEach,
    describe,
    expect,
    test,
    vi,
} from 'vitest';

import { useThemeStore } from '../theme';
import { Toolbar } from './toolbar';

function renderToolbar() {
    render(
        <Toolbar
            templates={[]}
            examples={[]}
            onSelectTemplate={() => {}}
            onSelectExample={() => {}}
            projectModified={false}
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

describe('Toolbar project menus', () => {
    beforeAll(() => {
        // jsdom has no modal dialog support, so opening one sets the `open`
        // attribute the way a browser does.
        HTMLDialogElement.prototype.showModal = function () {
            this.setAttribute('open', '');
        };
        HTMLDialogElement.prototype.close = function () {
            this.removeAttribute('open');
        };
    });

    afterEach(() => {
        cleanup();
    });

    function renderProjectMenus(projectModified: boolean) {
        const onSelectTemplate = vi.fn();
        const onSelectExample = vi.fn();
        render(
            <Toolbar
                templates={[{ slug: 'basic', label: 'Basic' }]}
                examples={[{ slug: 'enums', label: 'Enums' }]}
                onSelectTemplate={onSelectTemplate}
                onSelectExample={onSelectExample}
                projectModified={projectModified}
            />,
        );
        return { onSelectTemplate, onSelectExample };
    }

    test('loads an example without asking when the project is unmodified', () => {
        const { onSelectExample } = renderProjectMenus(false);

        fireEvent.click(screen.getByRole('button', { name: /Examples/ }));
        fireEvent.click(screen.getByRole('menuitem', { name: 'Enums' }));

        expect(onSelectExample).toHaveBeenCalledWith('enums');
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });

    test('loads a template without asking when the project is unmodified', () => {
        const { onSelectTemplate } = renderProjectMenus(false);

        fireEvent.click(screen.getByRole('button', { name: /New Project/ }));
        fireEvent.click(screen.getByRole('menuitem', { name: 'Basic' }));

        expect(onSelectTemplate).toHaveBeenCalledWith('basic');
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });

    test('asks before replacing a modified project', () => {
        const { onSelectExample } = renderProjectMenus(true);

        fireEvent.click(screen.getByRole('button', { name: /Examples/ }));
        fireEvent.click(screen.getByRole('menuitem', { name: 'Enums' }));

        expect(onSelectExample).not.toHaveBeenCalled();
        expect(screen.getByRole('dialog')).toBeInTheDocument();

        fireEvent.click(screen.getByRole('button', { name: 'Replace' }));

        expect(onSelectExample).toHaveBeenCalledWith('enums');
    });
});
