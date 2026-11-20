// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

import type { EditorState, Side } from './editor-state';
import { initialEditorState } from './editor-state';
import { useEditorStore } from './editor-store';
import { TabList } from './tab-list';

/** Set the store state for a test, preserving the real dispatch. */
function setStoreState(overrides: Partial<EditorState>): void {
    useEditorStore.setState({ ...initialEditorState, ...overrides });
}

/** Renders side's tab list from the editor store's state. */
function renderTabList(side: Side): void {
    const Harness = () => {
        const state = useEditorStore();
        return (
            <TabList
                side={side}
                tabs={side === 'left' ? state.leftTabs : state.rightTabs}
                activeIndex={
                    side === 'left'
                        ? state.activeLeftTabIndex
                        : state.activeRightTabIndex
                }
                isFocused={state.focusedSide === side}
                isDropTarget={false}
                dropHandlers={{
                    onDragOver: vi.fn(),
                    onDragLeave: vi.fn(),
                    onDrop: vi.fn(),
                }}
                onDragStart={vi.fn()}
                onDragEnd={vi.fn()}
            />
        );
    };
    render(<Harness />);
}

/** Returns the paths of side's tabs in the store. */
function tabPaths(side: Side): string[] {
    const state = useEditorStore.getState();
    return (side === 'left' ? state.leftTabs : state.rightTabs).map(
        (tab) => tab.path,
    );
}

beforeEach(() => {
    useEditorStore.setState(useEditorStore.getInitialState(), true);
});

afterEach(cleanup);

describe('rendering', () => {
    test.each([
        {
            side: 'left',
            testId: 'input-tablist',
            panelId: 'input-panel',
        },
        {
            side: 'right',
            testId: 'output-tablist',
            panelId: 'output-panel',
        },
    ] as const)(
        'renders the $side tabs, marking the active one',
        ({ side, testId, panelId }) => {
            setStoreState(
                side === 'left'
                    ? {
                          leftTabs: [
                              { path: '/bin/main.esc' },
                              { path: '/b.esc' },
                          ],
                          activeLeftTabIndex: 1,
                      }
                    : {
                          rightTabs: [
                              { path: '/bin/main.esc' },
                              { path: '/b.esc' },
                          ],
                          activeRightTabIndex: 1,
                      },
            );
            renderTabList(side);

            const tabs = screen.getAllByRole('tab');
            expect(screen.getByRole('tablist')).toHaveAttribute(
                'data-testid',
                testId,
            );
            expect(tabs.map((tab) => tab.textContent)).toEqual([
                'main.esc×',
                'b.esc×',
            ]);
            expect(
                tabs.map((tab) => tab.getAttribute('aria-selected')),
            ).toEqual(['false', 'true']);
            expect(tabs[0]).toHaveAttribute('aria-controls', panelId);
        },
    );
});

describe('tab interactions', () => {
    test('clicking an inactive tab makes it active and focuses its side', () => {
        setStoreState({
            rightTabs: [{ path: '/build/a.js' }, { path: '/build/b.js' }],
            activeRightTabIndex: 0,
            focusedSide: 'left',
        });
        renderTabList('right');

        fireEvent.click(screen.getAllByRole('tab')[1]);

        const state = useEditorStore.getState();
        expect(state.activeRightTabIndex).toBe(1);
        expect(state.focusedSide).toBe('right');
    });

    test('the close button closes its tab', () => {
        setStoreState({
            leftTabs: [{ path: '/a.esc' }, { path: '/b.esc' }],
            activeLeftTabIndex: 0,
        });
        renderTabList('left');

        fireEvent.click(screen.getByRole('button', { name: 'Close a.esc' }));

        expect(tabPaths('left')).toEqual(['/b.esc']);
        expect(screen.getAllByRole('tab')).toHaveLength(1);
    });
});

describe('context menu', () => {
    test.each([
        { side: 'left', item: 'Move to Right' },
        { side: 'right', item: 'Move to Left' },
    ] as const)(
        'right-clicking a $side tab offers $item and Close',
        ({ side, item }) => {
            setStoreState(
                side === 'left'
                    ? { leftTabs: [{ path: '/a.esc' }], activeLeftTabIndex: 0 }
                    : {
                          rightTabs: [{ path: '/a.esc' }],
                          activeRightTabIndex: 0,
                      },
            );
            renderTabList(side);

            fireEvent.contextMenu(screen.getByRole('tab'));

            expect(
                screen
                    .getAllByRole('button')
                    .map((button) => button.textContent),
            ).toEqual(['×', item, 'Close']);
        },
    );

    test('Move to Right moves the tab to the right side', () => {
        setStoreState({
            leftTabs: [{ path: '/a.esc' }, { path: '/b.esc' }],
            activeLeftTabIndex: 0,
        });
        renderTabList('left');

        fireEvent.contextMenu(screen.getAllByRole('tab')[0]);
        fireEvent.click(screen.getByRole('button', { name: 'Move to Right' }));

        expect(tabPaths('left')).toEqual(['/b.esc']);
        expect(tabPaths('right')).toEqual(['/a.esc']);
    });

    test('Move to Left moves the tab to the left side', () => {
        setStoreState({
            leftTabs: [{ path: '/a.esc' }],
            activeLeftTabIndex: 0,
            rightTabs: [{ path: '/build/a.js' }],
            activeRightTabIndex: 0,
        });
        renderTabList('right');

        fireEvent.contextMenu(screen.getByRole('tab'));
        fireEvent.click(screen.getByRole('button', { name: 'Move to Left' }));

        expect(tabPaths('left')).toEqual(['/a.esc', '/build/a.js']);
        expect(tabPaths('right')).toEqual([]);
    });

    test('Close closes the tab and the menu', () => {
        setStoreState({
            leftTabs: [{ path: '/a.esc' }, { path: '/b.esc' }],
            activeLeftTabIndex: 0,
        });
        renderTabList('left');

        fireEvent.contextMenu(screen.getAllByRole('tab')[0]);
        fireEvent.click(screen.getByRole('button', { name: 'Close' }));

        expect(tabPaths('left')).toEqual(['/b.esc']);
        expect(
            screen.queryByRole('button', { name: 'Move to Right' }),
        ).not.toBeInTheDocument();
    });

    test('a click elsewhere closes the menu', () => {
        setStoreState({
            leftTabs: [{ path: '/a.esc' }],
            activeLeftTabIndex: 0,
        });
        renderTabList('left');

        fireEvent.contextMenu(screen.getByRole('tab'));
        fireEvent.pointerDown(document.body);

        expect(
            screen.queryByRole('button', { name: 'Move to Right' }),
        ).not.toBeInTheDocument();
    });

    test('dragging a tab closes its menu', () => {
        setStoreState({
            leftTabs: [{ path: '/a.esc' }],
            activeLeftTabIndex: 0,
        });
        renderTabList('left');
        const tab = screen.getByRole('tab');

        fireEvent.contextMenu(tab);
        fireEvent.dragStart(tab, {
            dataTransfer: { setData: vi.fn(), effectAllowed: '' },
        });

        expect(
            screen.queryByRole('button', { name: 'Move to Right' }),
        ).not.toBeInTheDocument();
    });
});
