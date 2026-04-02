import * as monaco from 'monaco-editor-core';
import {
    type ReactNode,
    useCallback,
    useEffect,
    useRef,
    useState,
} from 'react';

import { FileExplorer } from './components/file-explorer';
import { Toast } from './components/toast';
import { useEditorStore } from './editor-store';
import type { BrowserFS } from './fs/browser-fs';
import { languageID } from './language';

import styles from './editor.module.css';

/** Get the display name (filename) from a path. */
function displayName(path: string): string {
    return path.split('/').pop() ?? path;
}

/** Get the Monaco language for a file path. */
function languageForPath(path: string): string {
    if (path.endsWith('.esc')) return languageID;
    if (path.endsWith('.js')) return 'javascript';
    if (path.endsWith('.ts') || path.endsWith('.d.ts')) return 'typescript';
    if (path.endsWith('.json') || path.endsWith('.js.map')) return 'json';
    if (path.endsWith('.toml')) return 'plaintext';
    return 'plaintext';
}

/** Convert a filesystem path to a Monaco URI string. */
function pathToUri(path: string): string {
    return `file://${path}`;
}

type ContextMenuState = {
    tabPath: string;
    side: 'left' | 'right';
    x: number;
    y: number;
};

type TabItemProps = {
    path: string;
    isActive: boolean;
    isFocused: boolean;
    panelId: string;
    side: 'left' | 'right';
    contextMenu: ContextMenuState | null;
    onActivate: () => void;
    onClose: () => void;
    onMove: () => void;
    onContextMenu: (x: number, y: number) => void;
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
};

export const Editor = ({
    fs,
    isReadOnly,
    rightPaneVisible,
    rightPaneOverlay,
    banner,
    toolbar,
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
                const text = content ?? loadFileContent(fs, path) ?? '';
                model = monaco.editor.createModel(text, lang, uri);
            }
            return model;
        },
        [fs],
    );

    // Initialize editors
    useEffect(() => {
        const inputElem = inputDivRef.current;
        const outputElem = outputDivRef.current;

        if (!inputElem || !outputElem) return;

        const inputEditor = monaco.editor.create(inputElem, {
            theme: 'escalier-theme',
            bracketPairColorization: { enabled: true },
            fontSize: 14,
            automaticLayout: true,
            'semanticHighlighting.enabled': true,
            wordBasedSuggestions: 'off',
        });
        inputEditorRef.current = inputEditor;

        const outputEditor = monaco.editor.create(outputElem, {
            theme: 'escalier-theme',
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

        return () => {
            inputFocusDisposable.dispose();
            outputFocusDisposable.dispose();
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
        editor.updateOptions({ readOnly: isReadOnly?.(activePath) ?? false });
        editor.setModel(model);

        // Restore scroll position
        if (activeTab?.scrollPos) {
            editor.setScrollTop(activeTab.scrollPos);
        }
    }, [
        activePath,
        getOrCreateModel,
        activeTab?.scrollPos,
        isReadOnly,
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
        editor.updateOptions({
            readOnly: isReadOnly?.(activeRightPath) ?? false,
        });
        editor.setModel(model);

        // Restore scroll position
        if (activeRightTab?.scrollPos) {
            editor.setScrollTop(activeRightTab.scrollPos);
        }
    }, [
        activeRightPath,
        getOrCreateModel,
        activeRightTab?.scrollPos,
        isReadOnly,
    ]);

    // Listen for FS events to update Monaco models for open right tabs
    useEffect(() => {
        if (rightTabs.length === 0) return;

        const openRightPaths = new Set(rightTabs.map((t) => t.path));

        const listener = (event: import('./fs/fs-events').FSEvent) => {
            if (
                (event.type === 'change' || event.type === 'create') &&
                openRightPaths.has(event.path)
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
    }, [rightTabs, fs]);

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
            <div className={styles.inputTabs} role="tablist">
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
                    />
                ))}
            </div>

            {/* Output tabs */}
            <div className={styles.outputTabs} role="tablist">
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
                    />
                ))}
            </div>

            {/* Input editor */}
            <div
                id="input-panel"
                role="tabpanel"
                className={styles.editorPane}
                ref={inputDivRef}
                style={{
                    display: leftTabs.length === 0 ? 'none' : undefined,
                }}
            />

            {/* Empty state when no tabs are open */}
            {leftTabs.length === 0 && (
                <div className={styles.emptyState}>
                    Open a file from the explorer to start editing
                </div>
            )}

            {/* Output editor */}
            <div
                id="output-panel"
                role="tabpanel"
                className={styles.editorPane}
                ref={outputDivRef}
                style={{
                    display: showRightPane ? undefined : 'none',
                }}
            />

            {/* Right pane overlay (e.g. compile spinner) */}
            {!showRightPane && rightPaneOverlay && (
                <div className={styles.rightPaneOverlay}>
                    {rightPaneOverlay}
                </div>
            )}

            <Toast
                notification={state.notification}
                onDismiss={handleDismissNotification}
            />
        </div>
    );
};

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
