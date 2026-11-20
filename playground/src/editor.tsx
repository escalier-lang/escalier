import * as monaco from 'monaco-editor-core';
import {
    type DragEvent,
    type ReactNode,
    useCallback,
    useEffect,
    useRef,
    useState,
} from 'react';

import { CrashReportDialog } from './components/crash-report-dialog';
import { FileExplorer } from './components/file-explorer';
import { Toast, ToastButton } from './components/toast';
import { diagnosticsLanguageID } from './diagnostics-language';
import type { CrashReport, Side, TextPosition } from './editor-state';
import { useEditorStore } from './editor-store';
import type { BrowserFS } from './fs/browser-fs';
import { languageID } from './language';
import { collectProjectFiles } from './permalink';
import {
    DIAGNOSTICS_TAB_PATH,
    diagnosticLocation,
    projectDiagnosticsText,
} from './project-diagnostics';
import { useProjectDiagnosticsStore } from './project-diagnostics-store';
import { monacoThemeName, useThemeStore } from './theme';

import styles from './editor.module.css';

/** Get the display name (filename) from a path. */
function displayName(path: string): string {
    return path.split('/').pop() ?? path;
}

/** Returns the Monaco language id for a file path. */
export function languageForPath(path: string): string {
    if (path === DIAGNOSTICS_TAB_PATH) return diagnosticsLanguageID;
    if (path.endsWith('.esc')) return languageID;
    if (path.endsWith('.js')) return 'javascript';
    if (path.endsWith('.ts') || path.endsWith('.d.ts')) return 'typescript';
    if (path.endsWith('.json') || path.endsWith('.js.map')) return 'json';
    if (path.endsWith('.toml')) return 'toml';
    return 'plaintext';
}

/** Convert a filesystem path to a Monaco URI string. */
function pathToUri(path: string): string {
    return `file://${path}`;
}

type ContextMenuState = {
    tabPath: string;
    side: Side;
    x: number;
    y: number;
};

/**
 * The drag data type a tab sets. Monaco ignores it, so a tab dropped on an editor
 * inserts no text.
 */
const TAB_DRAG_TYPE = 'application/x-escalier-tab';

/** The tab being dragged. */
type DraggedTab = { side: Side; index: number };

type DropHandlers = {
    onDragOver: (e: DragEvent) => void;
    onDragLeave: (e: DragEvent) => void;
    onDrop: (e: DragEvent) => void;
};

type EmptyPaneProps = {
    side: Side;
    /** Whether files opened from the explorer open on this side. */
    isFocused: boolean;
    isDropTarget: boolean;
    dropHandlers: DropHandlers;
    onFocus: () => void;
};

/**
 * Fills a side that has no tabs. Clicking it makes the side the one files open on,
 * and a tab dragged from the other side can be dropped on it.
 */
const EmptyPane = ({
    side,
    isFocused,
    isDropTarget,
    dropHandlers,
    onFocus,
}: EmptyPaneProps) => (
    <button
        type="button"
        className={[
            styles.emptyState,
            side === 'left' ? styles.inputEmpty : styles.outputEmpty,
            isFocused ? styles.focusedEmpty : '',
            isDropTarget ? styles.dropTarget : '',
        ]
            .filter(Boolean)
            .join(' ')}
        onClick={onFocus}
        {...dropHandlers}
    >
        {isFocused
            ? 'Open a file from the explorer or drag a tab here'
            : 'Click to open files here, or drag a tab here'}
    </button>
);

type TabItemProps = {
    path: string;
    isActive: boolean;
    isFocused: boolean;
    panelId: string;
    side: Side;
    contextMenu: ContextMenuState | null;
    onActivate: () => void;
    onClose: () => void;
    onMove: () => void;
    onContextMenu: (x: number, y: number) => void;
    onDragStart: () => void;
    onDragEnd: () => void;
};

const TabItem = ({
    path,
    isActive,
    isFocused,
    panelId,
    side,
    contextMenu,
    onActivate,
    onClose,
    onMove,
    onContextMenu,
    onDragStart,
    onDragEnd,
}: TabItemProps) => {
    const name = displayName(path);
    const className = `${styles.tab} ${isActive && isFocused ? styles.activeTab : styles.visibleTab}`;
    const isMenuOpen =
        contextMenu !== null &&
        contextMenu.tabPath === path &&
        contextMenu.side === side;

    return (
        <div
            role="tab"
            tabIndex={0}
            aria-selected={isActive}
            aria-controls={panelId}
            className={className}
            onClick={onActivate}
            onKeyDown={(e) => {
                if (e.key === 'Enter' || e.key === ' ') onActivate();
            }}
            onContextMenu={(e) => {
                e.preventDefault();
                onContextMenu(e.clientX, e.clientY);
            }}
            draggable
            onDragStart={(e) => {
                // Firefox starts a drag only when the drag carries data.
                e.dataTransfer.setData(TAB_DRAG_TYPE, path);
                e.dataTransfer.effectAllowed = 'move';
                onDragStart();
            }}
            onDragEnd={onDragEnd}
        >
            <span className={styles.tabLabel}>{name}</span>
            <button
                type="button"
                className={styles.closeButton}
                onClick={(e) => {
                    e.stopPropagation();
                    onClose();
                }}
                aria-label={`Close ${name}`}
                tabIndex={0}
            >
                &times;
            </button>
            {isMenuOpen && (
                <div
                    className={styles.contextMenu}
                    style={{ left: contextMenu.x, top: contextMenu.y }}
                    onClick={(e) => e.stopPropagation()}
                    onPointerDown={(e) => e.stopPropagation()}
                    onKeyDown={(e) => e.stopPropagation()}
                >
                    <button
                        type="button"
                        className={styles.contextMenuItem}
                        onClick={() => onMove()}
                    >
                        {side === 'left' ? 'Move to Right' : 'Move to Left'}
                    </button>
                    <button
                        type="button"
                        className={styles.contextMenuItem}
                        onClick={() => onClose()}
                    >
                        Close
                    </button>
                </div>
            )}
        </div>
    );
};

export type EditorProps = {
    fs: BrowserFS;
    isReadOnly?: (path: string) => boolean;
    rightPaneVisible?: boolean;
    rightPaneOverlay?: ReactNode;
    banner?: ReactNode;
    toolbar?: ReactNode;
    /** Shown in the bar along the bottom of the playground. */
    statusBar?: ReactNode;
};

export const Editor = ({
    fs,
    isReadOnly,
    rightPaneVisible,
    rightPaneOverlay,
    banner,
    toolbar,
    statusBar,
}: EditorProps) => {
    const { dispatch, ...state } = useEditorStore();
    const inputDivRef = useRef<HTMLDivElement>(null);
    const outputDivRef = useRef<HTMLDivElement>(null);
    const inputEditorRef = useRef<monaco.editor.IStandaloneCodeEditor | null>(
        null,
    );
    const outputEditorRef = useRef<monaco.editor.IStandaloneCodeEditor | null>(
        null,
    );
    const [contextMenu, setContextMenu] = useState<ContextMenuState | null>(
        null,
    );
    const [draggedTab, setDraggedTab] = useState<DraggedTab | null>(null);
    const [dropSide, setDropSide] = useState<Side | null>(null);
    const {
        leftTabs,
        activeLeftTabIndex,
        rightTabs,
        activeRightTabIndex,
        focusedSide,
        refreshKey,
    } = state;

    const showRightPane = rightPaneVisible ?? rightTabs.length > 0;

    // Dismiss context menu on click or pointer-down elsewhere.
    // pointerdown covers both left- and right-clicks and fires before
    // contextmenu, so it dismisses the menu before a new one opens.
    // We intentionally omit 'contextmenu' because it would bubble from
    // the tab and immediately clear the menu that was just opened.
    useEffect(() => {
        if (!contextMenu) return;
        const dismiss = () => setContextMenu(null);
        window.addEventListener('click', dismiss);
        window.addEventListener('pointerdown', dismiss);
        return () => {
            window.removeEventListener('click', dismiss);
            window.removeEventListener('pointerdown', dismiss);
        };
    }, [contextMenu]);

    const activeTab =
        activeLeftTabIndex !== null ? leftTabs[activeLeftTabIndex] : null;
    const activePath = activeTab?.path ?? null;

    const activeRightTab =
        activeRightTabIndex !== null ? rightTabs[activeRightTabIndex] : null;
    const activeRightPath = activeRightTab?.path ?? null;

    // Get or create a Monaco model for a file path
    const getOrCreateModel = useCallback(
        (path: string, content?: string) => {
            const uri = monaco.Uri.parse(pathToUri(path));
            let model = monaco.editor.getModel(uri);
            if (!model) {
                const lang = languageForPath(path);
                // The diagnostics tab names no file. Its text comes from the project's
                // diagnostics, and the effect below keeps it up to date.
                const text =
                    path === DIAGNOSTICS_TAB_PATH
                        ? projectDiagnosticsText(
                              useProjectDiagnosticsStore.getState().diagnostics,
                          )
                        : (content ?? loadFileContent(fs, path) ?? '');
                model = monaco.editor.createModel(text, lang, uri);
            }
            return model;
        },
        [fs],
    );

    // The options an editor takes while it shows the file at path. Nothing can edit the
    // diagnostics tab, since its text comes from the language server, and its lines wrap
    // so a long message reads without scrolling sideways.
    const editorOptionsFor = useCallback(
        (path: string): monaco.editor.IEditorOptions => {
            const isDiagnostics = path === DIAGNOSTICS_TAB_PATH;
            return {
                readOnly: isDiagnostics || (isReadOnly?.(path) ?? false),
                wordWrap: isDiagnostics ? 'on' : 'off',
            };
        },
        [isReadOnly],
    );

    // Rewrites the diagnostics tab's text each time the project's diagnostics change.
    useEffect(
        () =>
            useProjectDiagnosticsStore.subscribe((current, previous) => {
                if (current.diagnostics === previous.diagnostics) return;
                const model = monaco.editor.getModel(
                    monaco.Uri.parse(pathToUri(DIAGNOSTICS_TAB_PATH)),
                );
                const text = projectDiagnosticsText(current.diagnostics);
                if (model && model.getValue() !== text) {
                    model.setValue(text);
                }
            }),
        [],
    );

    // Initialize editors
    useEffect(() => {
        const inputElem = inputDivRef.current;
        const outputElem = outputDivRef.current;

        if (!inputElem || !outputElem) return;

        const inputEditor = monaco.editor.create(inputElem, {
            theme: monacoThemeName(useThemeStore.getState().theme),
            bracketPairColorization: { enabled: true },
            fontSize: 14,
            automaticLayout: true,
            'semanticHighlighting.enabled': true,
            wordBasedSuggestions: 'off',
        });
        inputEditorRef.current = inputEditor;

        const outputEditor = monaco.editor.create(outputElem, {
            theme: monacoThemeName(useThemeStore.getState().theme),
            bracketPairColorization: { enabled: true },
            fontSize: 14,
            automaticLayout: true,
            readOnly: true,
        });
        outputEditorRef.current = outputEditor;

        const inputFocusDisposable = inputEditor.onDidFocusEditorWidget(() =>
            dispatch({ type: 'setFocusedSide', side: 'left' }),
        );
        const outputFocusDisposable = outputEditor.onDidFocusEditorWidget(() =>
            dispatch({ type: 'setFocusedSide', side: 'right' }),
        );

        // A click on a line of the diagnostics tab opens the file the line names, on
        // the same side, with the tab set to reveal the diagnostic. The model switch
        // below moves there. A drag
        // that selects text opens nothing, so the user can still copy a message.
        const openClickedDiagnostic =
            (editor: monaco.editor.IStandaloneCodeEditor, side: Side) =>
            (e: monaco.editor.IEditorMouseEvent) => {
                const model = editor.getModel();
                const lineNumber = e.target.position?.lineNumber;
                if (
                    model?.uri.path !== DIAGNOSTICS_TAB_PATH ||
                    lineNumber === undefined ||
                    !editor.getSelection()?.isEmpty()
                ) {
                    return;
                }
                const location = diagnosticLocation(
                    model.getLineContent(lineNumber),
                );
                if (location === null) return;
                dispatch({
                    type: 'openFile',
                    path: location.path,
                    side,
                    revealAt: {
                        lineNumber: location.line,
                        column: location.column,
                    },
                });
            };
        const inputClickDisposable = inputEditor.onMouseUp(
            openClickedDiagnostic(inputEditor, 'left'),
        );
        const outputClickDisposable = outputEditor.onMouseUp(
            openClickedDiagnostic(outputEditor, 'right'),
        );

        return () => {
            inputFocusDisposable.dispose();
            outputFocusDisposable.dispose();
            inputClickDisposable.dispose();
            outputClickDisposable.dispose();
            inputEditor.dispose();
            outputEditor.dispose();
            inputEditorRef.current = null;
            outputEditorRef.current = null;
        };
    }, [dispatch]);

    // Switch input editor model when active tab changes or project is switched.
    // refreshKey changes on project switch (resetTabs), which disposes all
    // stale models so they get recreated with fresh content from BrowserFS.
    const prevRefreshKeyRef = useRef(refreshKey);
    useEffect(() => {
        const editor = inputEditorRef.current;
        if (!editor) return;

        if (prevRefreshKeyRef.current !== refreshKey) {
            prevRefreshKeyRef.current = refreshKey;
            for (const model of monaco.editor.getModels()) {
                model.dispose();
            }
        }

        if (!activePath) {
            editor.setModel(null);
            return;
        }

        const model = getOrCreateModel(activePath);
        editor.updateOptions(editorOptionsFor(activePath));
        editor.setModel(model);

        // Restore scroll position
        if (activeTab?.scrollPos) {
            editor.setScrollTop(activeTab.scrollPos);
        }
    }, [
        activePath,
        getOrCreateModel,
        activeTab?.scrollPos,
        editorOptionsFor,
        refreshKey,
    ]);

    // Switch output editor model when active right tab changes
    useEffect(() => {
        const editor = outputEditorRef.current;
        if (!editor || !activeRightPath) {
            outputEditorRef.current?.setModel(null);
            return;
        }

        const model = getOrCreateModel(activeRightPath);
        editor.updateOptions(editorOptionsFor(activeRightPath));
        editor.setModel(model);

        // Restore scroll position
        if (activeRightTab?.scrollPos) {
            editor.setScrollTop(activeRightTab.scrollPos);
        }
    }, [
        activeRightPath,
        getOrCreateModel,
        activeRightTab?.scrollPos,
        editorOptionsFor,
    ]);

    // Moves each editor to its active tab's revealAt, then clears it. These run after the
    // model switches above, in the same commit, so the editor shows the tab's file by
    // then. Clearing revealAt leaves the model switches alone, so the tab's scroll
    // position isn't restored over the reveal.
    const leftRevealAt = activeTab?.revealAt;
    useEffect(() => {
        const editor = inputEditorRef.current;
        if (!editor || !activePath || !leftRevealAt) return;
        revealPosition(editor, leftRevealAt);
        dispatch({ type: 'revealed', side: 'left', path: activePath });
    }, [activePath, leftRevealAt, dispatch]);
    const rightRevealAt = activeRightTab?.revealAt;
    useEffect(() => {
        const editor = outputEditorRef.current;
        if (!editor || !activeRightPath || !rightRevealAt) return;
        revealPosition(editor, rightRevealAt);
        dispatch({ type: 'revealed', side: 'right', path: activeRightPath });
    }, [activeRightPath, rightRevealAt, dispatch]);

    // The language server checks the project once an Escalier file opens, and a file opens
    // when the editor makes a model for it. A model for every Escalier tab gets the
    // project checked whichever tab is active, as when a permalink restores the
    // diagnostics tab as the active one. When the diagnostics tab is open and no Escalier
    // tab is, the project's first Escalier file opens without a tab.
    useEffect(() => {
        const tabPaths = [...leftTabs, ...rightTabs].map((tab) => tab.path);
        const escalierPaths = tabPaths.filter((path) => path.endsWith('.esc'));
        if (
            escalierPaths.length === 0 &&
            tabPaths.includes(DIAGNOSTICS_TAB_PATH)
        ) {
            const first = firstEscalierFile(fs);
            if (first !== null) escalierPaths.push(first);
        }
        for (const path of escalierPaths) {
            getOrCreateModel(path);
        }
    }, [leftTabs, rightTabs, getOrCreateModel, fs]);

    // Listen for FS events to update the Monaco models of open read-only files. Those
    // are the build output, which the compiler rewrites. Every right tab counts, and a
    // read-only left tab counts too, such as a build file a permalink opened there.
    useEffect(() => {
        const watchedPaths = new Set([
            ...rightTabs.map((t) => t.path),
            ...leftTabs
                .map((t) => t.path)
                .filter((path) => isReadOnly?.(path) ?? false),
        ]);
        if (watchedPaths.size === 0) return;

        const listener = (event: import('./fs/fs-events').FSEvent) => {
            if (
                (event.type === 'change' || event.type === 'create') &&
                watchedPaths.has(event.path)
            ) {
                const content = loadFileContent(fs, event.path) ?? '';
                const uri = monaco.Uri.parse(pathToUri(event.path));
                const model = monaco.editor.getModel(uri);
                if (model && model.getValue() !== content) {
                    model.setValue(content);
                }
            }
        };

        fs.events.on(listener);
        return () => fs.events.off(listener);
    }, [leftTabs, rightTabs, isReadOnly, fs]);

    const handleFileOpen = useCallback(
        (path: string) => dispatch({ type: 'openFile', path }),
        [dispatch],
    );

    const handleFileDelete = useCallback(
        (path: string) => dispatch({ type: 'deleteFile', path }),
        [dispatch],
    );

    const handleFileRename = useCallback(
        (oldPath: string, newPath: string) =>
            dispatch({ type: 'renameFile', oldPath, newPath }),
        [dispatch],
    );

    const handleDismissNotification = useCallback(
        () => dispatch({ type: 'dismissNotification' }),
        [dispatch],
    );

    // The crash report the dialog shows, or null when it is closed. It is kept apart from
    // the toast, so dismissing the toast leaves an open report showing.
    const [shownCrashReport, setShownCrashReport] =
        useState<CrashReport | null>(null);
    const crashReport = state.notification?.crashReport;
    const handleShowCrashReport = useCallback(() => {
        if (crashReport) {
            setShownCrashReport(crashReport);
        }
    }, [crashReport]);

    // Counts the notifications shown so far, to key each one's Toast. A notification that
    // replaces one on screen then gets a fresh Toast, whose auto-dismiss timer starts over.
    const [keyedNotification, setKeyedNotification] = useState({
        notification: state.notification,
        key: 0,
    });
    if (keyedNotification.notification !== state.notification) {
        setKeyedNotification({
            notification: state.notification,
            key: keyedNotification.key + 1,
        });
    }

    /**
     * Returns the drag handlers that let an element on side take a tab dragged from the
     * other side. Dropping the tab moves it, as "Move to Right" and "Move to Left" do.
     */
    const dropHandlers = (side: Side): DropHandlers => {
        const accepts = draggedTab !== null && draggedTab.side !== side;
        return {
            onDragOver: (e: DragEvent) => {
                if (!accepts) return;
                // Cancelling dragover marks the element as a drop target.
                e.preventDefault();
                e.dataTransfer.dropEffect = 'move';
                setDropSide(side);
            },
            onDragLeave: (e: DragEvent) => {
                // dragleave also fires on moving between the element's children.
                if (e.currentTarget.contains(e.relatedTarget as Node | null)) {
                    return;
                }
                setDropSide(null);
            },
            onDrop: (e: DragEvent) => {
                if (!accepts) return;
                e.preventDefault();
                setDropSide(null);
                setDraggedTab(null);
                dispatch({
                    type: 'moveTab',
                    from: draggedTab.side,
                    index: draggedTab.index,
                });
            },
        };
    };

    const endTabDrag = () => {
        setDraggedTab(null);
        setDropSide(null);
    };

    return (
        <div className={styles['editor-root']}>
            {/* Banner (e.g. validation errors) */}
            {banner && <div className={styles.banner}>{banner}</div>}

            {/* Toolbar */}
            <div className={styles.toolbar}>{toolbar}</div>

            {/* File explorer */}
            <FileExplorer
                fs={fs}
                onFileOpen={handleFileOpen}
                onFileDelete={handleFileDelete}
                onFileRename={handleFileRename}
            />

            {/* Input tabs */}
            <div
                {...dropHandlers('left')}
                className={`${styles.inputTabs} ${dropSide === 'left' ? styles.dropTarget : ''}`}
                role="tablist"
                data-testid="input-tablist"
            >
                {leftTabs.map((tab, i) => (
                    <TabItem
                        key={tab.path}
                        path={tab.path}
                        isActive={i === activeLeftTabIndex}
                        isFocused={focusedSide === 'left'}
                        panelId="input-panel"
                        side="left"
                        contextMenu={contextMenu}
                        onActivate={() => {
                            dispatch({ type: 'setFocusedSide', side: 'left' });
                            dispatch({
                                type: 'setActiveTab',
                                side: 'left',
                                index: i,
                            });
                        }}
                        onClose={() => {
                            setContextMenu(null);
                            dispatch({
                                type: 'closeTab',
                                side: 'left',
                                index: i,
                            });
                        }}
                        onMove={() => {
                            setContextMenu(null);
                            dispatch({
                                type: 'moveTab',
                                from: 'left',
                                index: i,
                            });
                        }}
                        onContextMenu={(x, y) =>
                            setContextMenu({
                                tabPath: tab.path,
                                side: 'left',
                                x,
                                y,
                            })
                        }
                        onDragStart={() => {
                            setContextMenu(null);
                            setDraggedTab({ side: 'left', index: i });
                        }}
                        onDragEnd={endTabDrag}
                    />
                ))}
            </div>

            {/* Output tabs */}
            <div
                {...dropHandlers('right')}
                className={`${styles.outputTabs} ${dropSide === 'right' ? styles.dropTarget : ''}`}
                role="tablist"
                data-testid="output-tablist"
            >
                {rightTabs.map((tab, i) => (
                    <TabItem
                        key={tab.path}
                        path={tab.path}
                        isActive={i === activeRightTabIndex}
                        isFocused={focusedSide === 'right'}
                        panelId="output-panel"
                        side="right"
                        contextMenu={contextMenu}
                        onActivate={() => {
                            dispatch({ type: 'setFocusedSide', side: 'right' });
                            dispatch({
                                type: 'setActiveTab',
                                side: 'right',
                                index: i,
                            });
                        }}
                        onClose={() => {
                            setContextMenu(null);
                            dispatch({
                                type: 'closeTab',
                                side: 'right',
                                index: i,
                            });
                        }}
                        onMove={() => {
                            setContextMenu(null);
                            dispatch({
                                type: 'moveTab',
                                from: 'right',
                                index: i,
                            });
                        }}
                        onContextMenu={(x, y) =>
                            setContextMenu({
                                tabPath: tab.path,
                                side: 'right',
                                x,
                                y,
                            })
                        }
                        onDragStart={() => {
                            setContextMenu(null);
                            setDraggedTab({ side: 'right', index: i });
                        }}
                        onDragEnd={endTabDrag}
                    />
                ))}
            </div>

            {/* Input editor */}
            <div
                id="input-panel"
                role="tabpanel"
                className={styles.inputPane}
                ref={inputDivRef}
                style={{
                    display: leftTabs.length === 0 ? 'none' : undefined,
                }}
            />

            {leftTabs.length === 0 && (
                <EmptyPane
                    side="left"
                    isFocused={focusedSide === 'left'}
                    isDropTarget={dropSide === 'left'}
                    dropHandlers={dropHandlers('left')}
                    onFocus={() =>
                        dispatch({ type: 'setFocusedSide', side: 'left' })
                    }
                />
            )}

            {/* Output editor */}
            <div
                id="output-panel"
                role="tabpanel"
                className={styles.outputPane}
                ref={outputDivRef}
                style={{
                    display: showRightPane ? undefined : 'none',
                }}
            />

            {/* Right pane overlay (e.g. compile spinner) */}
            {!showRightPane && rightPaneOverlay && (
                // The overlay takes a dropped tab as the empty pane it covers would.
                <div
                    {...dropHandlers('right')}
                    className={`${styles.rightPaneOverlay} ${dropSide === 'right' ? styles.dropTarget : ''}`}
                >
                    {rightPaneOverlay}
                </div>
            )}
            {!showRightPane && !rightPaneOverlay && (
                <EmptyPane
                    side="right"
                    isFocused={focusedSide === 'right'}
                    isDropTarget={dropSide === 'right'}
                    dropHandlers={dropHandlers('right')}
                    onFocus={() =>
                        dispatch({ type: 'setFocusedSide', side: 'right' })
                    }
                />
            )}

            {statusBar && <div className={styles.statusBar}>{statusBar}</div>}

            {state.notification && (
                <Toast
                    key={keyedNotification.key}
                    type={state.notification.type}
                    // A crash toast stays up so the user has time to open its report.
                    persistent={Boolean(crashReport)}
                    onDismiss={handleDismissNotification}
                >
                    <span>{state.notification.message}</span>
                    {crashReport && (
                        <ToastButton onClick={handleShowCrashReport}>
                            Details
                        </ToastButton>
                    )}
                </Toast>
            )}
            {shownCrashReport && (
                <CrashReportDialog
                    report={shownCrashReport}
                    onClose={() => setShownCrashReport(null)}
                />
            )}
        </div>
    );
};

/** Puts editor's cursor at position, scrolls it to the centre and focuses editor. */
function revealPosition(
    editor: monaco.editor.IStandaloneCodeEditor,
    position: TextPosition,
): void {
    editor.setPosition(position);
    editor.revealPositionInCenter(position);
    editor.focus();
}

/**
 * Returns the path of the project's first Escalier file in path order, preferring one under
 * `/lib/`, or null when the project has none.
 */
function firstEscalierFile(fs: BrowserFS): string | null {
    const paths = Object.keys(collectProjectFiles(fs.rootDir, () => ''))
        .filter((path) => path.endsWith('.esc'))
        .sort();
    return paths.find((path) => path.startsWith('/lib/')) ?? paths[0] ?? null;
}

/**
 * Load file content from BrowserFS. BrowserFS callbacks fire synchronously
 * (it's all in-memory), so we can collect the result in a closure.
 * Returns null if the file doesn't exist.
 */
function loadFileContent(fs: BrowserFS, path: string): string | null {
    let result: string | null = null;

    let called = false;
    fs.open(path, 'r', undefined, (openErr, fd) => {
        called = true;
        if (openErr || fd === undefined) return;

        fs.fstat(fd, (statErr, stats) => {
            if (statErr) {
                fs.close(fd, () => {});
                return;
            }

            const buf = new Uint8Array(stats.size);
            fs.read(fd, buf, 0, stats.size, 0, (readErr) => {
                fs.close(fd, () => {});
                if (readErr) return;
                result = new TextDecoder().decode(buf);
            });
        });
    });

    if (!called) {
        throw new Error(
            'BrowserFS callback was not synchronous — loadFileContent requires synchronous callbacks',
        );
    }

    return result;
}
