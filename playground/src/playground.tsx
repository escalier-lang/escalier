import * as monaco from 'monaco-editor-core';
import { useCallback, useEffect, useRef, useState } from 'react';

import { FileExplorer } from './components/file-explorer';
import { Toast } from './components/toast';
import type { BrowserFS } from './fs/browser-fs';
import { languageID } from './language';
import {
    type OutputTab,
    usePlaygroundDispatch,
    usePlaygroundState,
} from './state';

import styles from './playground.module.css';

/**
 * Determine which output tabs to show for a given input file path.
 * - `.esc` under `lib/` => js, map, dts
 * - `.esc` under `bin/` => js, map
 * - anything else => none
 */
function getOutputTabs(path: string): OutputTab[] {
    if (!path.endsWith('.esc')) return [];
    if (path.startsWith('/lib/') || path === '/lib')
        return ['js', 'map', 'dts'];
    if (path.startsWith('/bin/') || path === '/bin') return ['js', 'map'];
    // Check for packages/*/lib/ or packages/*/bin/ patterns
    const match = path.match(/^\/packages\/[^/]+\/(lib|bin)\//);
    if (match) {
        return match[1] === 'lib' ? ['js', 'map', 'dts'] : ['js', 'map'];
    }
    return [];
}

/** Get the output file extension for an output tab type. */
function outputTabExtension(tab: OutputTab): string {
    switch (tab) {
        case 'js':
            return '.js';
        case 'map':
            return '.js.map';
        case 'dts':
            return '.d.ts';
    }
}

/** Convert a source .esc path to its corresponding output path. */
function sourceToOutputPath(sourcePath: string, tab: OutputTab): string {
    // Replace the source root (lib/ or bin/) with build/lib/ or build/bin/
    // and swap the extension.
    const ext = outputTabExtension(tab);
    const withoutEsc = sourcePath.replace(/\.esc$/, ext);

    // Handle packages/*/lib/ and packages/*/bin/
    const pkgMatch = withoutEsc.match(
        /^(\/packages\/[^/]+\/)(lib\/|bin\/)(.*)/,
    );
    if (pkgMatch) {
        return `${pkgMatch[1]}build/${pkgMatch[2]}${pkgMatch[3]}`;
    }

    // Handle root lib/ and bin/
    if (withoutEsc.startsWith('/lib/') || withoutEsc.startsWith('/bin/')) {
        return `/build${withoutEsc}`;
    }

    return withoutEsc;
}

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
    const [focusedSide, setFocusedSide] = useState<'input' | 'output'>('input');

    const { openTabs, activeTabIndex, activeOutputTab, validationResult } =
        state;
    const activeTab = activeTabIndex !== null ? openTabs[activeTabIndex] : null;
    const activePath = activeTab?.path ?? null;

    // Determine available output tabs for the active file
    const availableOutputTabs = activePath ? getOutputTabs(activePath) : [];
    const showOutput = availableOutputTabs.length > 0;

    // Ensure the active output tab is valid for the current file
    const effectiveOutputTab = availableOutputTabs.includes(activeOutputTab)
        ? activeOutputTab
        : (availableOutputTabs[0] ?? 'js');

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
            setFocusedSide('input'),
        );
        const outputFocusDisposable = outputEditor.onDidFocusEditorWidget(() =>
            setFocusedSide('output'),
        );

        return () => {
            inputFocusDisposable.dispose();
            outputFocusDisposable.dispose();
            inputEditor.dispose();
            outputEditor.dispose();
            inputEditorRef.current = null;
            outputEditorRef.current = null;
        };
    }, []);

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

    // Switch output editor model when active output tab or input file changes
    useEffect(() => {
        const editor = outputEditorRef.current;
        if (!editor || !activePath || !showOutput) {
            outputEditorRef.current?.setModel(null);
            return;
        }

        const outputPath = sourceToOutputPath(activePath, effectiveOutputTab);
        const uri = monaco.Uri.parse(pathToUri(outputPath));
        let model = monaco.editor.getModel(uri);
        if (!model) {
            // No model yet (compile hasn't run or produced this output).
            // Try loading from BrowserFS as fallback.
            const content = loadFileContent(fs, outputPath) ?? '';
            const lang = languageForPath(outputPath);
            model = monaco.editor.createModel(content, lang, uri);
        }
        editor.setModel(model);
    }, [activePath, effectiveOutputTab, showOutput, fs]);

    // Listen for FS events on build/ paths to refresh output
    useEffect(() => {
        if (!activePath || !showOutput) return;

        const listener = (event: import('./fs/fs-events').FSEvent) => {
            if (
                event.path.startsWith('/build/') ||
                /^\/packages\/[^/]+\/build\//.test(event.path)
            ) {
                const outputPath = sourceToOutputPath(
                    activePath,
                    effectiveOutputTab,
                );
                if (event.path === outputPath) {
                    const content = loadFileContent(fs, outputPath) ?? '';
                    const uri = monaco.Uri.parse(pathToUri(outputPath));
                    const model = monaco.editor.getModel(uri);
                    if (model && model.getValue() !== content) {
                        model.setValue(content);
                    }
                }
            }
        };

        fs.events.on(listener);
        return () => fs.events.off(listener);
    }, [activePath, effectiveOutputTab, showOutput, fs]);

    const handleCloseTab = (e: React.MouseEvent, index: number) => {
        e.stopPropagation();
        dispatch({ type: 'closeTab', index });
    };

    const handleTabClick = (index: number) => {
        setFocusedSide('input');
        dispatch({ type: 'setActiveTab', index });
    };

    const handleOutputTabClick = (tab: OutputTab) => {
        setFocusedSide('output');
        dispatch({ type: 'setActiveOutputTab', tab });
    };

    const outputTabLabel = (tab: OutputTab): string => {
        if (!activePath) return '';
        const name = displayName(activePath).replace(/\.esc$/, '');
        return `${name}${outputTabExtension(tab)}`;
    };

    const tabClass = (isActive: boolean, isFocused: boolean) =>
        `${styles.tab} ${isActive && isFocused ? styles.activeTab : styles.visibleTab}`;

    return (
        <div className={styles.playground}>
            {/* Toolbar area - height: 0 for now, expanded in Phase 6 */}
            <div className={styles.toolbar} />

            {/* File explorer */}
            <FileExplorer fs={fs} />

            {/* Input tabs */}
            <div className={styles.inputTabs} role="tablist">
                {openTabs.map((tab, i) => (
                    <div
                        key={tab.path}
                        role="tab"
                        tabIndex={0}
                        aria-selected={i === activeTabIndex}
                        aria-controls="input-panel"
                        className={tabClass(
                            i === activeTabIndex,
                            focusedSide === 'input',
                        )}
                        onClick={() => handleTabClick(i)}
                        onKeyDown={(e) => {
                            if (e.key === 'Enter' || e.key === ' ')
                                handleTabClick(i);
                        }}
                    >
                        <span className={styles.tabLabel}>
                            {displayName(tab.path)}
                        </span>
                        <button
                            type="button"
                            className={styles.closeButton}
                            onClick={(e) => handleCloseTab(e, i)}
                            aria-label={`Close ${displayName(tab.path)}`}
                            tabIndex={-1}
                        >
                            &times;
                        </button>
                    </div>
                ))}
            </div>

            {/* Output tabs */}
            <div
                className={styles.outputTabs}
                role="tablist"
                style={{ visibility: showOutput ? 'visible' : 'hidden' }}
            >
                {availableOutputTabs.map((tab) => (
                    <div
                        key={tab}
                        role="tab"
                        tabIndex={0}
                        aria-selected={tab === effectiveOutputTab}
                        aria-controls="output-panel"
                        className={tabClass(
                            tab === effectiveOutputTab,
                            focusedSide === 'output',
                        )}
                        onClick={() => handleOutputTabClick(tab)}
                        onKeyDown={(e) => {
                            if (e.key === 'Enter' || e.key === ' ')
                                handleOutputTabClick(tab);
                        }}
                    >
                        {outputTabLabel(tab)}
                    </div>
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
                    visibility: showOutput ? 'visible' : 'hidden',
                }}
            />

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
