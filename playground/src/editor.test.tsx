// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest';
import {
    act,
    cleanup,
    fireEvent,
    render,
    screen,
} from '@testing-library/react';
import {
    afterEach,
    beforeAll,
    beforeEach,
    describe,
    expect,
    test,
    vi,
} from 'vitest';

// Mock monaco-editor-core — uses the auto mock in __mocks__/monaco-editor-core.ts.
// vi.mock calls are hoisted by vitest automatically.
vi.mock('monaco-editor-core');

// Mock the language module to avoid Monaco language registration side effects.
vi.mock('./language', () => ({ languageID: 'escalier' }));

import * as monaco from 'monaco-editor-core';

import { stubModalDialogs } from './components/dialog-stub';
import { Editor, languageForPath } from './editor';
import type { EditorState } from './editor-state';
import { initialEditorState } from './editor-state';
import { useEditorStore } from './editor-store';
import type { BrowserFS } from './fs/browser-fs';
import { FSEventEmitter } from './fs/fs-events';
import {
    DIAGNOSTICS_TAB_PATH,
    type ProjectDiagnostic,
} from './project-diagnostics';
import { useProjectDiagnosticsStore } from './project-diagnostics-store';

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

beforeAll(stubModalDialogs);
afterEach(() => {
    cleanup();
    vi.useRealTimers();
});

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
            ).toBeInTheDocument();
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

            expect(screen.getByText('Move to Right')).toBeInTheDocument();
            expect(screen.getByText('Close')).toBeInTheDocument();
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

            expect(screen.getByText('Move to Left')).toBeInTheDocument();
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

    describe('dragging a tab', () => {
        /** Returns a dataTransfer stand-in that records what a drag sets. */
        const dataTransfer = () => ({
            setData: vi.fn(),
            effectAllowed: '',
            dropEffect: '',
        });

        test('dropping a left tab on the right tab list moves it there', () => {
            setStoreState({
                leftTabs: [{ path: '/a.esc' }, { path: '/b.esc' }],
                activeLeftTabIndex: 0,
            });
            render(<Editor fs={makeFakeFS()} />);
            const transfer = dataTransfer();
            const rightList = screen.getByTestId('output-tablist');

            fireEvent.dragStart(getTabsInTablist(0)[0], {
                dataTransfer: transfer,
            });
            fireEvent.dragOver(rightList, { dataTransfer: transfer });
            expect(rightList.className).toContain('dropTarget');
            fireEvent.drop(rightList, { dataTransfer: transfer });

            expect(transfer.setData).toHaveBeenCalledWith(
                'application/x-escalier-tab',
                '/a.esc',
            );
            expect(rightList.className).not.toContain('dropTarget');
            const state = useEditorStore.getState();
            expect(state.leftTabs).toEqual([{ path: '/b.esc' }]);
            expect(state.rightTabs).toEqual([{ path: '/a.esc' }]);
            expect(state.activeRightTabIndex).toBe(0);
        });

        test('dropping a tab on its own tab list leaves it in place', () => {
            setStoreState({
                leftTabs: [{ path: '/a.esc' }],
                activeLeftTabIndex: 0,
                rightTabs: [{ path: '/build/a.js' }],
                activeRightTabIndex: 0,
            });
            render(<Editor fs={makeFakeFS()} />);
            const transfer = dataTransfer();
            const rightList = screen.getByTestId('output-tablist');

            fireEvent.dragStart(getTabsInTablist(1)[0], {
                dataTransfer: transfer,
            });
            fireEvent.dragOver(rightList, { dataTransfer: transfer });
            fireEvent.drop(rightList, { dataTransfer: transfer });

            expect(rightList.className).not.toContain('dropTarget');
            const state = useEditorStore.getState();
            expect(state.leftTabs).toEqual([{ path: '/a.esc' }]);
            expect(state.rightTabs).toEqual([{ path: '/build/a.js' }]);
        });

        test('ending a drag outside a tab list moves nothing', () => {
            setStoreState({
                leftTabs: [{ path: '/a.esc' }],
                activeLeftTabIndex: 0,
            });
            render(<Editor fs={makeFakeFS()} />);
            const transfer = dataTransfer();
            const tab = getTabsInTablist(0)[0];
            const rightList = screen.getByTestId('output-tablist');

            fireEvent.dragStart(tab, { dataTransfer: transfer });
            fireEvent.dragOver(rightList, { dataTransfer: transfer });
            fireEvent.dragLeave(rightList, {
                dataTransfer: transfer,
                relatedTarget: document.body,
            });
            expect(rightList.className).not.toContain('dropTarget');
            fireEvent.dragEnd(tab, { dataTransfer: transfer });
            fireEvent.drop(rightList, { dataTransfer: transfer });

            expect(useEditorStore.getState().rightTabs).toEqual([]);
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
            ).toBeInTheDocument();
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

            expect(screen.getByTestId('error-banner')).toBeInTheDocument();
            expect(screen.getByText('Build failed')).toBeInTheDocument();
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

            expect(screen.getByTestId('spinner')).toBeInTheDocument();
            expect(screen.getByText('Compiling...')).toBeInTheDocument();
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

            expect(screen.queryByTestId('spinner')).not.toBeInTheDocument();
        });
    });

    describe('notification', () => {
        test('renders toast when notification is set', () => {
            setStoreState({
                notification: { message: 'Saved!', type: 'info' },
            });
            render(<Editor fs={makeFakeFS()} />);

            expect(screen.getByText('Saved!')).toBeInTheDocument();
            expect(screen.getByRole('alert')).toBeInTheDocument();
        });

        test('clicking dismiss removes the toast', () => {
            setStoreState({
                notification: { message: 'Saved!', type: 'info' },
            });
            render(<Editor fs={makeFakeFS()} />);

            expect(screen.getByRole('alert')).toBeInTheDocument();

            fireEvent.click(
                screen.getByRole('button', { name: 'Dismiss notification' }),
            );

            expect(screen.queryByRole('alert')).not.toBeInTheDocument();
        });

        test('a crash toast stays up and its Details button opens the report', () => {
            vi.useFakeTimers();
            setStoreState({
                notification: {
                    message: 'The language server crashed and was restarted.',
                    type: 'warning',
                    crashReport: {
                        headline: 'panic: boom',
                        text: 'panic: boom\n',
                    },
                },
            });
            render(<Editor fs={makeFakeFS()} />);

            act(() => {
                vi.advanceTimersByTime(60_000);
            });
            fireEvent.click(screen.getByRole('button', { name: 'Details' }));
            fireEvent.click(
                screen.getByRole('button', { name: 'Dismiss notification' }),
            );

            // Dismissing the toast leaves the report open.
            expect(screen.queryByRole('alert')).not.toBeInTheDocument();
            expect(screen.getByLabelText('Crash report')).toHaveTextContent(
                'panic: boom',
            );
        });

        test('a notification that replaces another gets the full four seconds', () => {
            vi.useFakeTimers();
            setStoreState({
                notification: { message: 'Saved!', type: 'info' },
            });
            render(<Editor fs={makeFakeFS()} />);

            act(() => {
                vi.advanceTimersByTime(3000);
            });
            act(() => {
                useEditorStore.getState().dispatch({
                    type: 'showNotification',
                    notification: { message: 'Saved again!', type: 'info' },
                });
            });
            act(() => {
                vi.advanceTimersByTime(3000);
            });

            expect(screen.getByRole('alert')).toHaveTextContent('Saved again!');
            act(() => {
                vi.advanceTimersByTime(1000);
            });
            expect(screen.queryByRole('alert')).not.toBeInTheDocument();
        });
    });
});

describe('diagnostics tab', () => {
    const error: ProjectDiagnostic = {
        path: '/lib/index.esc',
        line: 1,
        column: 2,
        severity: 'error',
        message: 'bad',
    };

    beforeEach(() => {
        useProjectDiagnosticsStore.setState({
            diagnostics: { '/lib/index.esc': [error] },
        });
    });

    afterEach(() => {
        vi.mocked(monaco.editor.getModel).mockReset();
        vi.mocked(monaco.editor.createModel).mockClear();
    });

    test('shows every diagnostic, read-only and wrapped, in the diagnostics language', () => {
        setStoreState({
            leftTabs: [{ path: DIAGNOSTICS_TAB_PATH }],
            activeLeftTabIndex: 0,
        });
        render(<Editor fs={makeFakeFS()} />);

        expect(monaco.editor.createModel).toHaveBeenCalledWith(
            '/lib/index.esc:1:2: error: bad\n',
            'escalier-diagnostics',
            `file://${DIAGNOSTICS_TAB_PATH}`,
        );
        const inputEditor = vi.mocked(monaco.editor.create).mock.results.at(-2)
            ?.value as { updateOptions: ReturnType<typeof vi.fn> };
        expect(inputEditor.updateOptions).toHaveBeenCalledWith({
            readOnly: true,
            wordWrap: 'on',
        });
    });

    test('rewrites its text when the diagnostics change', () => {
        const model = { getValue: vi.fn(() => 'old\n'), setValue: vi.fn() };
        vi.mocked(monaco.editor.getModel).mockImplementation((uri) =>
            String(uri) === `file://${DIAGNOSTICS_TAB_PATH}`
                ? (model as unknown as monaco.editor.ITextModel)
                : null,
        );
        render(<Editor fs={makeFakeFS()} />);

        act(() => {
            useProjectDiagnosticsStore.getState().dispatch({
                type: 'setFileDiagnostics',
                path: '/lib/index.esc',
                diagnostics: [],
            });
        });

        expect(model.setValue).toHaveBeenCalledWith('No diagnostics.\n');
    });
});

describe('clicking a diagnostic', () => {
    type MockEditor = {
        onMouseUp: ReturnType<typeof vi.fn>;
        getModel: ReturnType<typeof vi.fn>;
        getSelection: ReturnType<typeof vi.fn>;
        setPosition: ReturnType<typeof vi.fn>;
        revealPositionInCenter: ReturnType<typeof vi.fn>;
        focus: ReturnType<typeof vi.fn>;
    };

    test('opens the file on the same side and reveals the diagnostic', () => {
        setStoreState({
            leftTabs: [{ path: DIAGNOSTICS_TAB_PATH }],
            activeLeftTabIndex: 0,
        });
        render(<Editor fs={makeFakeFS()} />);
        const inputEditor = vi.mocked(monaco.editor.create).mock.results.at(-2)
            ?.value as MockEditor;
        inputEditor.getModel.mockReturnValue({
            uri: { path: DIAGNOSTICS_TAB_PATH },
            getLineContent: () => '/lib/index.esc:3:7: error: bad',
        });
        inputEditor.getSelection.mockReturnValue({ isEmpty: () => true });
        const [onMouseUp] = inputEditor.onMouseUp.mock.calls[0];

        act(() => onMouseUp({ target: { position: { lineNumber: 1 } } }));

        const { leftTabs, activeLeftTabIndex } = useEditorStore.getState();
        expect(leftTabs.map((tab) => tab.path)).toEqual([
            DIAGNOSTICS_TAB_PATH,
            '/lib/index.esc',
        ]);
        expect(activeLeftTabIndex).toBe(1);
        const position = { lineNumber: 3, column: 7 };
        expect(inputEditor.setPosition).toHaveBeenCalledWith(position);
        expect(inputEditor.revealPositionInCenter).toHaveBeenCalledWith(
            position,
        );
        expect(inputEditor.focus).toHaveBeenCalled();
        // The editor clears the reveal once it has moved there.
        expect(useEditorStore.getState().leftTabs[1]).toEqual({
            path: '/lib/index.esc',
        });
    });

    test('opens nothing after a drag that selects text', () => {
        setStoreState({
            leftTabs: [{ path: DIAGNOSTICS_TAB_PATH }],
            activeLeftTabIndex: 0,
        });
        render(<Editor fs={makeFakeFS()} />);
        const inputEditor = vi.mocked(monaco.editor.create).mock.results.at(-2)
            ?.value as MockEditor;
        inputEditor.getModel.mockReturnValue({
            uri: { path: DIAGNOSTICS_TAB_PATH },
            getLineContent: () => '/lib/index.esc:3:7: error: bad',
        });
        inputEditor.getSelection.mockReturnValue({ isEmpty: () => false });
        const [onMouseUp] = inputEditor.onMouseUp.mock.calls[0];

        act(() => onMouseUp({ target: { position: { lineNumber: 1 } } }));

        expect(useEditorStore.getState().leftTabs).toHaveLength(1);
    });
});

describe('language server models', () => {
    afterEach(() => {
        vi.mocked(monaco.editor.createModel).mockClear();
    });

    /** Returns the paths Editor made models for. */
    const modelPaths = () =>
        vi
            .mocked(monaco.editor.createModel)
            .mock.calls.map(([, , uri]) => String(uri).replace('file://', ''));

    test('opens every Escalier tab, not only the active one', () => {
        setStoreState({
            leftTabs: [
                { path: DIAGNOSTICS_TAB_PATH },
                { path: '/lib/index.esc' },
            ],
            activeLeftTabIndex: 0,
            rightTabs: [{ path: '/bin/main.esc' }, { path: '/build/a.js' }],
            activeRightTabIndex: 1,
        });
        render(<Editor fs={makeFakeFS()} />);

        expect(modelPaths()).toEqual(
            expect.arrayContaining(['/lib/index.esc', '/bin/main.esc']),
        );
    });

    test("opens the project's first Escalier file when only the diagnostics tab is open", () => {
        const file = (name: string) => ({
            type: 'file' as const,
            name,
            content: new Uint8Array(),
        });
        const dir = (name: string, children: { name: string }[]) => ({
            type: 'dir' as const,
            name,
            children: new Map(children.map((child) => [child.name, child])),
        });
        const fakeFS = makeFakeFS();
        (fakeFS as { rootDir: unknown }).rootDir = dir('/', [
            dir('bin', [file('main.esc')]),
            dir('lib', [file('index.esc')]),
            file('package.json'),
        ]);
        setStoreState({
            leftTabs: [{ path: DIAGNOSTICS_TAB_PATH }],
            activeLeftTabIndex: 0,
        });
        render(<Editor fs={fakeFS} />);

        expect(modelPaths()).toEqual([DIAGNOSTICS_TAB_PATH, '/lib/index.esc']);
    });
});

describe('languageForPath', () => {
    test.each([
        { path: '/lib/index.esc', language: 'escalier' },
        { path: '/build/lib/index.js', language: 'javascript' },
        { path: '/build/lib/index.d.ts', language: 'typescript' },
        { path: '/package.json', language: 'json' },
        { path: '/build/lib/index.js.map', language: 'json' },
        { path: '/escalier.toml', language: 'toml' },
        { path: '/README.md', language: 'plaintext' },
    ])('maps $path to $language', ({ path, language }) => {
        expect(languageForPath(path)).toBe(language);
    });
});
