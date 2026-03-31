// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

// Mock monaco-editor-core — uses the auto mock in __mocks__/monaco-editor-core.ts.
// vi.mock calls are hoisted by vitest automatically.
vi.mock('monaco-editor-core');

// Mock the language module to avoid Monaco language registration side effects.
vi.mock('./language', () => ({ languageID: 'escalier' }));

import { Editor } from './editor';
import type { EditorState } from './editor-state';
import { initialEditorState } from './editor-state';
import { useEditorStore } from './editor-store';
import type { BrowserFS } from './fs/browser-fs';
import { FSEventEmitter } from './fs/fs-events';

function makeFakeFS(): BrowserFS {
    return {
        rootDir: { type: 'dir', name: '/', children: new Map() },
        events: new FSEventEmitter(),
        // open must call back synchronously (loadFileContent relies on this).
        // Returning an error signals "file not found", which is fine for tests.
        open: vi.fn(
            (
                _path: string,
                _flags: string,
                _mode: unknown,
                cb: (err: Error | null) => void,
            ) => cb(new Error('ENOENT')),
        ),
    } as unknown as BrowserFS;
}

/** Set the store state for a test, preserving the real dispatch. */
function setStoreState(overrides: Partial<EditorState>): void {
    useEditorStore.setState({ ...initialEditorState, ...overrides });
}

/** Helper to get tabs within a specific tablist (0 = left, 1 = right). */
function getTabsInTablist(index: number): NodeListOf<Element> {
    const tablists = screen.getAllByRole('tablist');
    return tablists[index].querySelectorAll('[role="tab"]');
}

afterEach(cleanup);

beforeEach(() => {
    // Reset store to initial state before each test.
    // Use the store's initialState to get a fresh dispatch function.
    useEditorStore.setState(useEditorStore.getInitialState(), true);
});

describe('Editor', () => {
    describe('tab rendering', () => {
        test('renders left tabs from state', () => {
            setStoreState({
                leftTabs: [
                    { path: '/bin/main.esc' },
                    { path: '/lib/utils.esc' },
                ],
                activeLeftTabIndex: 0,
            });
            render(<Editor fs={makeFakeFS()} />);

            const tabs = getTabsInTablist(0);
            expect(tabs).toHaveLength(2);
            expect(tabs[0].textContent).toContain('main.esc');
            expect(tabs[1].textContent).toContain('utils.esc');
        });

        test('renders right tabs from state', () => {
            setStoreState({
                rightTabs: [
                    { path: '/build/bin/main.js' },
                    { path: '/build/bin/main.d.ts' },
                ],
                activeRightTabIndex: 0,
            });
            render(<Editor fs={makeFakeFS()} />);

            const tabs = getTabsInTablist(1);
            expect(tabs).toHaveLength(2);
            expect(tabs[0].textContent).toContain('main.js');
            expect(tabs[1].textContent).toContain('main.d.ts');
        });

        test('marks the active left tab as selected', () => {
            setStoreState({
                leftTabs: [{ path: '/a.esc' }, { path: '/b.esc' }],
                activeLeftTabIndex: 1,
            });
            render(<Editor fs={makeFakeFS()} />);

            const tabs = screen.getAllByRole('tab');
            expect(tabs[0].getAttribute('aria-selected')).toBe('false');
            expect(tabs[1].getAttribute('aria-selected')).toBe('true');
        });
    });

    describe('tab interactions', () => {
        test('clicking an inactive tab makes it active', () => {
            setStoreState({
                leftTabs: [{ path: '/a.esc' }, { path: '/b.esc' }],
                activeLeftTabIndex: 0,
            });
            render(<Editor fs={makeFakeFS()} />);

            const tabs = screen.getAllByRole('tab');
            expect(tabs[0].getAttribute('aria-selected')).toBe('true');
            expect(tabs[1].getAttribute('aria-selected')).toBe('false');

            fireEvent.click(tabs[1]);

            expect(tabs[0].getAttribute('aria-selected')).toBe('false');
            expect(tabs[1].getAttribute('aria-selected')).toBe('true');
        });

        test('closing a tab removes it from the DOM', () => {
            setStoreState({
                leftTabs: [{ path: '/a.esc' }, { path: '/b.esc' }],
                activeLeftTabIndex: 0,
            });
            render(<Editor fs={makeFakeFS()} />);

            expect(getTabsInTablist(0)).toHaveLength(2);

            const closeButton = screen.getByRole('button', {
                name: 'Close a.esc',
            });
            fireEvent.click(closeButton);

            const remainingTabs = getTabsInTablist(0);
            expect(remainingTabs).toHaveLength(1);
            expect(remainingTabs[0].textContent).toContain('b.esc');
        });

        test('closing the last tab shows empty state', () => {
            setStoreState({
                leftTabs: [{ path: '/a.esc' }],
                activeLeftTabIndex: 0,
            });
            render(<Editor fs={makeFakeFS()} />);

            fireEvent.click(
                screen.getByRole('button', { name: 'Close a.esc' }),
            );

            expect(getTabsInTablist(0)).toHaveLength(0);
            expect(
                screen.getByText(
                    'Open a file from the explorer to start editing',
                ),
            ).toBeTruthy();
        });

        test('clicking an inactive right tab makes it active', () => {
            setStoreState({
                rightTabs: [{ path: '/build/a.js' }, { path: '/build/b.js' }],
                activeRightTabIndex: 0,
            });
            render(<Editor fs={makeFakeFS()} />);

            const tabs = getTabsInTablist(1);
            expect(tabs[0].getAttribute('aria-selected')).toBe('true');
            expect(tabs[1].getAttribute('aria-selected')).toBe('false');

            fireEvent.click(tabs[1]);

            expect(tabs[0].getAttribute('aria-selected')).toBe('false');
            expect(tabs[1].getAttribute('aria-selected')).toBe('true');
        });
    });

    describe('context menu', () => {
        test('right-clicking a tab shows context menu with Move and Close', () => {
            setStoreState({
                leftTabs: [{ path: '/a.esc' }],
                activeLeftTabIndex: 0,
            });
            render(<Editor fs={makeFakeFS()} />);

            fireEvent.contextMenu(screen.getAllByRole('tab')[0]);

            expect(screen.getByText('Move to Right')).toBeTruthy();
            expect(screen.getByText('Close')).toBeTruthy();
        });

        test('Move to Right moves the tab from left to right tablist', () => {
            setStoreState({
                leftTabs: [{ path: '/a.esc' }, { path: '/b.esc' }],
                activeLeftTabIndex: 0,
            });
            render(<Editor fs={makeFakeFS()} />);

            // Move first tab to right
            fireEvent.contextMenu(getTabsInTablist(0)[0]);
            fireEvent.click(screen.getByText('Move to Right'));

            // Left should now only have b.esc
            const leftTabs = getTabsInTablist(0);
            expect(leftTabs).toHaveLength(1);
            expect(leftTabs[0].textContent).toContain('b.esc');

            // Right should now have a.esc
            const rightTabs = getTabsInTablist(1);
            expect(rightTabs).toHaveLength(1);
            expect(rightTabs[0].textContent).toContain('a.esc');
        });

        test('right tab context menu shows Move to Left', () => {
            setStoreState({
                rightTabs: [{ path: '/build/a.js' }],
                activeRightTabIndex: 0,
            });
            render(<Editor fs={makeFakeFS()} />);

            const rightTab = getTabsInTablist(1)[0];
            fireEvent.contextMenu(rightTab);

            expect(screen.getByText('Move to Left')).toBeTruthy();
        });

        test('Move to Left moves the tab from right to left tablist', () => {
            setStoreState({
                leftTabs: [{ path: '/a.esc' }],
                activeLeftTabIndex: 0,
                rightTabs: [{ path: '/build/a.js' }],
                activeRightTabIndex: 0,
            });
            render(<Editor fs={makeFakeFS()} />);

            fireEvent.contextMenu(getTabsInTablist(1)[0]);
            fireEvent.click(screen.getByText('Move to Left'));

            // Left should now have both tabs
            const leftTabs = getTabsInTablist(0);
            expect(leftTabs).toHaveLength(2);
            expect(leftTabs[1].textContent).toContain('a.js');

            // Right should be empty
            expect(getTabsInTablist(1)).toHaveLength(0);
        });

        test('Close in context menu removes the tab', () => {
            setStoreState({
                leftTabs: [{ path: '/a.esc' }, { path: '/b.esc' }],
                activeLeftTabIndex: 0,
            });
            render(<Editor fs={makeFakeFS()} />);

            fireEvent.contextMenu(getTabsInTablist(0)[0]);
            fireEvent.click(screen.getByText('Close'));

            const leftTabs = getTabsInTablist(0);
            expect(leftTabs).toHaveLength(1);
            expect(leftTabs[0].textContent).toContain('b.esc');
        });
    });

    describe('empty state', () => {
        test('shows empty state message when no left tabs are open', () => {
            setStoreState({
                leftTabs: [],
                activeLeftTabIndex: null,
            });
            render(<Editor fs={makeFakeFS()} />);

            expect(
                screen.getByText(
                    'Open a file from the explorer to start editing',
                ),
            ).toBeTruthy();
        });

        test('hides input panel when no left tabs are open', () => {
            setStoreState({
                leftTabs: [],
                activeLeftTabIndex: null,
            });
            render(<Editor fs={makeFakeFS()} />);

            const inputPanel = document.getElementById('input-panel');
            expect(inputPanel?.style.display).toBe('none');
        });
    });

    describe('banner prop', () => {
        test('renders banner when provided', () => {
            render(
                <Editor
                    fs={makeFakeFS()}
                    banner={<div data-testid="error-banner">Build failed</div>}
                />,
            );

            expect(screen.getByTestId('error-banner')).toBeTruthy();
            expect(screen.getByText('Build failed')).toBeTruthy();
        });

        test('does not render banner wrapper when not provided', () => {
            const { container } = render(<Editor fs={makeFakeFS()} />);

            expect(container.querySelector('[class*="banner"]')).toBeNull();
        });
    });

    describe('right pane visibility', () => {
        test('shows right pane when rightPaneVisible is true', () => {
            setStoreState({
                rightTabs: [{ path: '/build/a.js' }],
                activeRightTabIndex: 0,
            });
            render(<Editor fs={makeFakeFS()} rightPaneVisible={true} />);

            const outputPanel = document.getElementById('output-panel');
            expect(outputPanel?.style.display).not.toBe('none');
        });

        test('hides right pane when rightPaneVisible is false', () => {
            setStoreState({
                rightTabs: [{ path: '/build/a.js' }],
                activeRightTabIndex: 0,
            });
            render(<Editor fs={makeFakeFS()} rightPaneVisible={false} />);

            const outputPanel = document.getElementById('output-panel');
            expect(outputPanel?.style.display).toBe('none');
        });

        test('renders right pane overlay when pane is hidden', () => {
            render(
                <Editor
                    fs={makeFakeFS()}
                    rightPaneVisible={false}
                    rightPaneOverlay={
                        <div data-testid="spinner">Compiling...</div>
                    }
                />,
            );

            expect(screen.getByTestId('spinner')).toBeTruthy();
            expect(screen.getByText('Compiling...')).toBeTruthy();
        });

        test('does not render overlay when right pane is visible', () => {
            setStoreState({
                rightTabs: [{ path: '/build/a.js' }],
                activeRightTabIndex: 0,
            });
            render(
                <Editor
                    fs={makeFakeFS()}
                    rightPaneVisible={true}
                    rightPaneOverlay={
                        <div data-testid="spinner">Compiling...</div>
                    }
                />,
            );

            expect(screen.queryByTestId('spinner')).toBeNull();
        });
    });

    describe('notification', () => {
        test('renders toast when notification is set', () => {
            setStoreState({
                notification: { message: 'Saved!', type: 'info' },
            });
            render(<Editor fs={makeFakeFS()} />);

            expect(screen.getByText('Saved!')).toBeTruthy();
            expect(screen.getByRole('alert')).toBeTruthy();
        });

        test('clicking dismiss removes the toast', () => {
            setStoreState({
                notification: { message: 'Saved!', type: 'info' },
            });
            render(<Editor fs={makeFakeFS()} />);

            expect(screen.getByRole('alert')).toBeTruthy();

            fireEvent.click(
                screen.getByRole('button', { name: 'Dismiss notification' }),
            );

            expect(screen.queryByRole('alert')).toBeNull();
        });
    });
});
