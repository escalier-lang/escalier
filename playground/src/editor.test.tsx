// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';

// Mock monaco-editor-core — uses the auto mock in __mocks__/monaco-editor-core.ts.
// vi.mock calls are hoisted by vitest automatically.
vi.mock('monaco-editor-core');

// Mock the language module to avoid Monaco language registration side effects.
vi.mock('./language', () => ({ languageID: 'escalier' }));

import { Editor } from './editor';
import type { EditorState } from './editor-state';
import { initialEditorState } from './editor-state';
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

function stateWith(overrides: Partial<EditorState> = {}): EditorState {
    return { ...initialEditorState, ...overrides };
}

afterEach(cleanup);

describe('Editor', () => {
    describe('tab rendering', () => {
        test('renders left tabs from state', () => {
            const state = stateWith({
                leftTabs: [
                    { path: '/bin/main.esc' },
                    { path: '/lib/utils.esc' },
                ],
                activeLeftTabIndex: 0,
            });
            render(
                <Editor fs={makeFakeFS()} state={state} dispatch={vi.fn()} />,
            );

            const tablists = screen.getAllByRole('tablist');
            const leftTablist = tablists[0];
            const tabs = leftTablist.querySelectorAll('[role="tab"]');

            expect(tabs).toHaveLength(2);
            expect(tabs[0].textContent).toContain('main.esc');
            expect(tabs[1].textContent).toContain('utils.esc');
        });

        test('renders right tabs from state', () => {
            const state = stateWith({
                rightTabs: [
                    { path: '/build/bin/main.js' },
                    { path: '/build/bin/main.d.ts' },
                ],
                activeRightTabIndex: 0,
            });
            render(
                <Editor fs={makeFakeFS()} state={state} dispatch={vi.fn()} />,
            );

            const tablists = screen.getAllByRole('tablist');
            const rightTablist = tablists[1];
            const tabs = rightTablist.querySelectorAll('[role="tab"]');

            expect(tabs).toHaveLength(2);
            expect(tabs[0].textContent).toContain('main.js');
            expect(tabs[1].textContent).toContain('main.d.ts');
        });

        test('marks the active left tab as selected', () => {
            const state = stateWith({
                leftTabs: [{ path: '/a.esc' }, { path: '/b.esc' }],
                activeLeftTabIndex: 1,
            });
            render(
                <Editor fs={makeFakeFS()} state={state} dispatch={vi.fn()} />,
            );

            const tabs = screen.getAllByRole('tab');
            expect(tabs[0].getAttribute('aria-selected')).toBe('false');
            expect(tabs[1].getAttribute('aria-selected')).toBe('true');
        });
    });

    describe('dispatch on tab interactions', () => {
        test('clicking a left tab dispatches setFocusedSide and setActiveTab', () => {
            const dispatch = vi.fn();
            const state = stateWith({
                leftTabs: [{ path: '/a.esc' }, { path: '/b.esc' }],
                activeLeftTabIndex: 0,
            });
            render(
                <Editor fs={makeFakeFS()} state={state} dispatch={dispatch} />,
            );

            const tabs = screen.getAllByRole('tab');
            fireEvent.click(tabs[1]);

            expect(dispatch).toHaveBeenCalledWith({
                type: 'setFocusedSide',
                side: 'left',
            });
            expect(dispatch).toHaveBeenCalledWith({
                type: 'setActiveTab',
                side: 'left',
                index: 1,
            });
        });

        test('clicking the close button dispatches closeTab', () => {
            const dispatch = vi.fn();
            const state = stateWith({
                leftTabs: [{ path: '/a.esc' }],
                activeLeftTabIndex: 0,
            });
            render(
                <Editor fs={makeFakeFS()} state={state} dispatch={dispatch} />,
            );

            const closeButton = screen.getByRole('button', {
                name: 'Close a.esc',
            });
            fireEvent.click(closeButton);

            expect(dispatch).toHaveBeenCalledWith({
                type: 'closeTab',
                side: 'left',
                index: 0,
            });
        });

        test('clicking a right tab dispatches setFocusedSide and setActiveTab for right side', () => {
            const dispatch = vi.fn();
            const state = stateWith({
                rightTabs: [{ path: '/build/a.js' }, { path: '/build/b.js' }],
                activeRightTabIndex: 0,
            });
            render(
                <Editor fs={makeFakeFS()} state={state} dispatch={dispatch} />,
            );

            const tablists = screen.getAllByRole('tablist');
            const rightTabs = tablists[1].querySelectorAll('[role="tab"]');
            fireEvent.click(rightTabs[1]);

            expect(dispatch).toHaveBeenCalledWith({
                type: 'setFocusedSide',
                side: 'right',
            });
            expect(dispatch).toHaveBeenCalledWith({
                type: 'setActiveTab',
                side: 'right',
                index: 1,
            });
        });
    });

    describe('context menu', () => {
        test('right-clicking a tab shows context menu with Move and Close', () => {
            const state = stateWith({
                leftTabs: [{ path: '/a.esc' }],
                activeLeftTabIndex: 0,
            });
            render(
                <Editor fs={makeFakeFS()} state={state} dispatch={vi.fn()} />,
            );

            const tab = screen.getAllByRole('tab')[0];
            fireEvent.contextMenu(tab);

            expect(screen.getByText('Move to Right')).toBeTruthy();
            expect(screen.getByText('Close')).toBeTruthy();
        });

        test('clicking Move to Right dispatches moveTab', () => {
            const dispatch = vi.fn();
            const state = stateWith({
                leftTabs: [{ path: '/a.esc' }],
                activeLeftTabIndex: 0,
            });
            render(
                <Editor fs={makeFakeFS()} state={state} dispatch={dispatch} />,
            );

            const tab = screen.getAllByRole('tab')[0];
            fireEvent.contextMenu(tab);
            fireEvent.click(screen.getByText('Move to Right'));

            expect(dispatch).toHaveBeenCalledWith({
                type: 'moveTab',
                from: 'left',
                index: 0,
            });
        });

        test('right tab context menu shows Move to Left', () => {
            const state = stateWith({
                rightTabs: [{ path: '/build/a.js' }],
                activeRightTabIndex: 0,
            });
            render(
                <Editor fs={makeFakeFS()} state={state} dispatch={vi.fn()} />,
            );

            const tablists = screen.getAllByRole('tablist');
            const rightTab = tablists[1].querySelector('[role="tab"]');
            expect(rightTab).not.toBeNull();
            fireEvent.contextMenu(rightTab as Element);

            expect(screen.getByText('Move to Left')).toBeTruthy();
        });
    });

    describe('empty state', () => {
        test('shows empty state message when no left tabs are open', () => {
            const state = stateWith({
                leftTabs: [],
                activeLeftTabIndex: null,
            });
            render(
                <Editor fs={makeFakeFS()} state={state} dispatch={vi.fn()} />,
            );

            expect(
                screen.getByText(
                    'Open a file from the explorer to start editing',
                ),
            ).toBeTruthy();
        });

        test('hides input panel when no left tabs are open', () => {
            const state = stateWith({
                leftTabs: [],
                activeLeftTabIndex: null,
            });
            render(
                <Editor fs={makeFakeFS()} state={state} dispatch={vi.fn()} />,
            );

            const inputPanel = document.getElementById('input-panel');
            expect(inputPanel?.style.display).toBe('none');
        });
    });

    describe('banner prop', () => {
        test('renders banner when provided', () => {
            render(
                <Editor
                    fs={makeFakeFS()}
                    state={stateWith()}
                    dispatch={vi.fn()}
                    banner={<div data-testid="error-banner">Build failed</div>}
                />,
            );

            expect(screen.getByTestId('error-banner')).toBeTruthy();
            expect(screen.getByText('Build failed')).toBeTruthy();
        });

        test('does not render banner wrapper when not provided', () => {
            const { container } = render(
                <Editor
                    fs={makeFakeFS()}
                    state={stateWith()}
                    dispatch={vi.fn()}
                />,
            );

            expect(container.querySelector('[class*="banner"]')).toBeNull();
        });
    });

    describe('right pane visibility', () => {
        test('shows right pane when rightPaneVisible is true', () => {
            const state = stateWith({
                rightTabs: [{ path: '/build/a.js' }],
                activeRightTabIndex: 0,
            });
            render(
                <Editor
                    fs={makeFakeFS()}
                    state={state}
                    dispatch={vi.fn()}
                    rightPaneVisible={true}
                />,
            );

            const outputPanel = document.getElementById('output-panel');
            expect(outputPanel?.style.display).not.toBe('none');
        });

        test('hides right pane when rightPaneVisible is false', () => {
            const state = stateWith({
                rightTabs: [{ path: '/build/a.js' }],
                activeRightTabIndex: 0,
            });
            render(
                <Editor
                    fs={makeFakeFS()}
                    state={state}
                    dispatch={vi.fn()}
                    rightPaneVisible={false}
                />,
            );

            const outputPanel = document.getElementById('output-panel');
            expect(outputPanel?.style.display).toBe('none');
        });

        test('renders right pane overlay when pane is hidden', () => {
            render(
                <Editor
                    fs={makeFakeFS()}
                    state={stateWith()}
                    dispatch={vi.fn()}
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
            const state = stateWith({
                rightTabs: [{ path: '/build/a.js' }],
                activeRightTabIndex: 0,
            });
            render(
                <Editor
                    fs={makeFakeFS()}
                    state={state}
                    dispatch={vi.fn()}
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
        test('passes notification to Toast', () => {
            const state = stateWith({
                notification: { message: 'Saved!', type: 'info' },
            });
            render(
                <Editor fs={makeFakeFS()} state={state} dispatch={vi.fn()} />,
            );

            expect(screen.getByText('Saved!')).toBeTruthy();
            expect(screen.getByRole('alert')).toBeTruthy();
        });

        test('clicking dismiss dispatches dismissNotification', () => {
            const dispatch = vi.fn();
            const state = stateWith({
                notification: { message: 'Saved!', type: 'info' },
            });
            render(
                <Editor fs={makeFakeFS()} state={state} dispatch={dispatch} />,
            );

            fireEvent.click(
                screen.getByRole('button', { name: 'Dismiss notification' }),
            );

            expect(dispatch).toHaveBeenCalledWith({
                type: 'dismissNotification',
            });
        });
    });
});
