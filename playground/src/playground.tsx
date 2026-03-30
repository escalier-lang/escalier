import * as monaco from 'monaco-editor-core';
import { useCallback, useEffect, useRef, useState } from 'react';

import { FileExplorer } from './components/file-explorer';
import { Toast } from './components/toast';
import type { BrowserFS } from './fs/browser-fs';
import { languageID } from './language';
import { usePlaygroundDispatch, usePlaygroundState } from './state';

import styles from './playground.module.css';

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
    return `file:///home/user/project${path}`;
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

type PlaygroundProps = {
    fs: BrowserFS;
};

export const Playground = ({ fs }: PlaygroundProps) => {
    const state = usePlaygroundState();
    const dispatch = usePlaygroundDispatch();

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
        leftTabs: openTabs,
        activeLeftTabIndex: activeTabIndex,
        rightTabs,
        activeRightTabIndex,
        focusedSide,
        initialCompileDone,
        validationResult,
    } = state;

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

    const activeTab = activeTabIndex !== null ? openTabs[activeTabIndex] : null;
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

    // Switch input editor model when active tab changes
    useEffect(() => {
        const editor = inputEditorRef.current;
        if (!editor) return;

        if (!activePath) {
            editor.setModel(null);
            return;
        }

        const model = getOrCreateModel(activePath);
        const isBuildFile =
            activePath.startsWith('/build/') ||
            /^\/packages\/[^/]+\/build\//.test(activePath);
        editor.updateOptions({ readOnly: !!isBuildFile });
        editor.setModel(model);

        // Restore scroll position
        if (activeTab?.scrollPos) {
            editor.setScrollTop(activeTab.scrollPos);
        }
    }, [activePath, getOrCreateModel, activeTab?.scrollPos]);

    // Switch output editor model when active right tab changes
    useEffect(() => {
        const editor = outputEditorRef.current;
        if (!editor || !activeRightPath) {
            outputEditorRef.current?.setModel(null);
            return;
        }

        const model = getOrCreateModel(activeRightPath);
        const isBuildFile =
            activeRightPath.startsWith('/build/') ||
            /^\/packages\/[^/]+\/build\//.test(activeRightPath);
        editor.updateOptions({ readOnly: !!isBuildFile });
        editor.setModel(model);

        // Restore scroll position
        if (activeRightTab?.scrollPos) {
            editor.setScrollTop(activeRightTab.scrollPos);
        }
    }, [activeRightPath, getOrCreateModel, activeRightTab?.scrollPos]);

    // Listen for FS events on build/ paths to update Monaco models for open right tabs
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

    // After initial compilation, open the build output files as right tabs.
    // Only auto-open build files whose source is currently open on the left.
    useEffect(() => {
        if (initialCompileDone) return;

        // Build a set of expected output prefixes from the open left-side
        // .esc files. The compiler maps all outputs under /build/ at the root:
        //   "/bin/main.esc" → "/build/bin/main."
        //   "/packages/foo/lib/bar.esc" → "/build/packages/foo/lib/bar."
        const buildPrefixes = new Set<string>();
        for (const tab of openTabs) {
            if (!tab.path.endsWith('.esc')) continue;
            buildPrefixes.add(`/build${tab.path.replace(/\.esc$/, '.')}`);
        }

        let markedDone = false;
        const listener = (event: import('./fs/fs-events').FSEvent) => {
            if (
                (event.type !== 'create' && event.type !== 'change') ||
                event.kind !== 'file'
            )
                return;

            const isBuildFile =
                event.path.startsWith('/build/') ||
                /^\/packages\/[^/]+\/build\//.test(event.path);
            if (!isBuildFile) return;

            // Only auto-open if it matches a left-side source file
            const matchesSource = [...buildPrefixes].some((prefix) =>
                event.path.startsWith(prefix),
            );
            if (matchesSource) {
                dispatch({ type: 'openFile', path: event.path, side: 'right' });
            }

            // Mark compilation done after the first batch of build files.
            // queueMicrotask ensures all synchronous FS writes from one
            // compilation are captured before we mark done.
            if (!markedDone) {
                markedDone = true;
                queueMicrotask(() => {
                    dispatch({ type: 'setInitialCompileDone' });
                });
            }
        };

        fs.events.on(listener);
        return () => fs.events.off(listener);
    }, [initialCompileDone, openTabs, fs, dispatch]);

    return (
        <div className={styles.playground}>
            {/* Toolbar area - height: 0 for now, expanded in Phase 6 */}
            <div className={styles.toolbar} />

            {/* File explorer */}
            <FileExplorer fs={fs} />

            {/* Input tabs */}
            <div className={styles.inputTabs} role="tablist">
                {openTabs.map((tab, i) => (
                    <TabItem
                        key={tab.path}
                        path={tab.path}
                        isActive={i === activeTabIndex}
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

            {/* Validation error banner */}
            {validationResult.mode === 'invalid' && (
                <div className={styles.errorBanner}>
                    {validationResult.errors.map((err, i) => (
                        <div key={`${i}-${err}`}>{err}</div>
                    ))}
                </div>
            )}

            {/* Input editor */}
            <div
                id="input-panel"
                role="tabpanel"
                className={styles.editor}
                ref={inputDivRef}
                style={{
                    display: openTabs.length === 0 ? 'none' : undefined,
                }}
            />

            {/* Empty state when no tabs are open */}
            {openTabs.length === 0 && (
                <div className={styles.emptyState}>
                    Open a file from the explorer to start editing
                </div>
            )}

            {/* Output editor */}
            <div
                id="output-panel"
                role="tabpanel"
                className={styles.editor}
                ref={outputDivRef}
                style={{
                    display:
                        rightTabs.length === 0 || !initialCompileDone
                            ? 'none'
                            : undefined,
                }}
            />

            {/* Spinner while waiting for initial compilation */}
            {!initialCompileDone && (
                <div className={styles.compileSpinner}>
                    <div className={styles.spinner} />
                    <span>Compiling...</span>
                </div>
            )}

            <Toast />
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
