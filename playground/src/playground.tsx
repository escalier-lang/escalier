import { useCallback, useEffect, useMemo, useRef, useState } from 'react';

import {
    type ShownError,
    playgroundEnvironment,
} from './bug-report/bug-report';
import { shownErrors } from './bug-report/editor-errors';
import { BugReportDialog } from './components/bug-report-dialog';
import { MessageDialog } from './components/message-dialog';
import { StatusBar } from './components/status-bar';
import { Toolbar } from './components/toolbar';
import { Editor } from './editor/editor';
import { useEditorStore } from './editor/editor-store';
import type { BrowserFS } from './fs/browser-fs';
import type { Manifest } from './fs/volume';
import { usePlaygroundStore } from './playground-store';
import { capturePermalink } from './project/capture-permalink';
import { MAX_PERMALINK_CHARS } from './project/permalink';
import { isBuildPath, loadProject } from './project/project-loader';

import styles from './playground.module.css';

/**
 * Returns the menu label for a project slug in sentence case, so
 * `overloads-and-generators` becomes `Overloads and generators`.
 */
export function slugToLabel(slug: string): string {
    const words = slug.split('-').join(' ');
    return words.charAt(0).toUpperCase() + words.slice(1);
}

/**
 * Returns what the dialog says when a project's link would be length characters,
 * longer than MAX_PERMALINK_CHARS.
 */
export function oversizedLinkMessage(length: number): string {
    const format = (n: number) => n.toLocaleString('en-US');
    return `A link to this project would be ${format(length)} characters long, but the playground makes links of at most ${format(MAX_PERMALINK_CHARS)} characters. Remove the files and code that the problem doesn't need, then share again.`;
}

type PlaygroundProps = {
    fs: BrowserFS;
    manifest: Manifest;
    baseUrl: string;
    /**
     * True when the tabs came from a permalink. The playground then keeps the tabs as
     * they are instead of opening the build output of the open source files.
     */
    layoutRestored?: boolean;
};

export const Playground = ({
    fs,
    manifest,
    baseUrl,
    layoutRestored = false,
}: PlaygroundProps) => {
    const { dispatch: editorDispatch, ...editorState } = useEditorStore();
    const { dispatch: playgroundDispatch, ...playgroundState } =
        usePlaygroundStore();
    const { initialCompileDone, validationResult } = playgroundState;

    const { leftTabs, rightTabs } = editorState;

    // Whether the next compile opens the build output of the open source files. A
    // permalink's tabs already list the build files that were open, so loading one
    // turns this off until the user loads another project.
    const autoOpenBuildFilesRef = useRef(!layoutRestored);

    // After initial compilation, open the build output files as right tabs.
    // Only auto-open build files whose source is currently open on the left.
    useEffect(() => {
        if (initialCompileDone) return;
        if (!autoOpenBuildFilesRef.current) {
            // Restored tabs show without waiting for the compile, since a project
            // shared for a bug report may never produce build output. The editor
            // fills an open build tab in when the compiler writes its file.
            playgroundDispatch({ type: 'setInitialCompileDone' });
            return;
        }

        // Build a set of expected output prefixes from the open left-side
        // .esc files. The compiler maps all outputs under /build/ at the root:
        //   "/bin/main.esc" → "/build/bin/main."
        //   "/packages/foo/lib/bar.esc" → "/build/packages/foo/lib/bar."
        const buildPrefixes = new Set<string>();
        for (const tab of leftTabs) {
            if (!tab.path.endsWith('.esc')) continue;
            buildPrefixes.add(`/build${tab.path.replace(/\.esc$/, '.')}`);
        }

        let cancelled = false;
        let markedDone = false;

        const openBuildFile = (path: string) => {
            editorDispatch({
                type: 'openFile',
                path,
                side: 'right',
            });
            if (!markedDone) {
                markedDone = true;
                queueMicrotask(() => {
                    if (!cancelled) {
                        playgroundDispatch({ type: 'setInitialCompileDone' });
                    }
                });
            }
        };

        const listener = (event: import('./fs/fs-events').FSEvent) => {
            if (
                (event.type !== 'create' && event.type !== 'change') ||
                event.kind !== 'file'
            )
                return;

            if (!isBuildPath(event.path)) return;

            // Only auto-open if it matches a left-side source file
            const matchesSource = [...buildPrefixes].some((prefix) =>
                event.path.startsWith(prefix),
            );
            if (matchesSource) {
                openBuildFile(event.path);
            }
        };

        fs.events.on(listener);

        // Check for build files that were written before the listener was
        // set up. This handles the race where the WASM LSP compiles and
        // writes output before React runs this effect.
        const suffixes = ['.js', '.js.map', '.d.ts'];
        for (const prefix of buildPrefixes) {
            for (const suffix of suffixes) {
                const path = prefix + suffix;
                fs.stat(path, (err) => {
                    if (!err && !cancelled) {
                        openBuildFile(path);
                    }
                });
            }
        }

        return () => {
            cancelled = true;
            fs.events.off(listener);
        };
    }, [initialCompileDone, leftTabs, fs, editorDispatch, playgroundDispatch]);

    const templateItems = useMemo(
        () =>
            Object.keys(manifest.templates).map((slug) => ({
                slug,
                label: slugToLabel(slug),
            })),
        [manifest],
    );

    const exampleItems = useMemo(
        () =>
            Object.keys(manifest.examples).map((slug) => ({
                slug,
                label: slugToLabel(slug),
            })),
        [manifest],
    );

    // Each call to handleSelectTemplate/handleSelectExample increments this
    // counter and captures the new value. When the async loadProject resolves,
    // the .then() callback only updates UI (resetTabs, URL) if its captured id
    // still matches the current ref — meaning no newer selection has started.
    // This prevents a slow earlier load from overwriting a faster later one.
    const loadIdRef = useRef(0);

    const handleSelectTemplate = useCallback(
        (slug: string) => {
            const id = ++loadIdRef.current;
            autoOpenBuildFilesRef.current = true;
            playgroundDispatch({ type: 'resetCompile' });
            loadProject(slug, 'template', manifest, baseUrl, fs).then(
                (primaryFile) => {
                    if (id !== loadIdRef.current) return;
                    editorDispatch({ type: 'resetTabs', primaryFile });
                    // Clear the query param when switching to a template
                    history.replaceState(null, '', window.location.pathname);
                },
                (err: unknown) => {
                    if (id !== loadIdRef.current) return;
                    playgroundDispatch({ type: 'setInitialCompileDone' });
                    editorDispatch({
                        type: 'showNotification',
                        notification: {
                            message: `Failed to load template: ${err instanceof Error ? err.message : String(err)}`,
                            type: 'error',
                        },
                    });
                },
            );
        },
        [manifest, baseUrl, fs, editorDispatch, playgroundDispatch],
    );

    const handleSelectExample = useCallback(
        (slug: string) => {
            const id = ++loadIdRef.current;
            autoOpenBuildFilesRef.current = true;
            playgroundDispatch({ type: 'resetCompile' });
            loadProject(slug, 'example', manifest, baseUrl, fs).then(
                (primaryFile) => {
                    if (id !== loadIdRef.current) return;
                    editorDispatch({ type: 'resetTabs', primaryFile });
                    // Update URL query param for deep linking, and drop any
                    // permalink so a reload shows the example.
                    const url = new URL(window.location.href);
                    url.searchParams.set('example', slug);
                    url.hash = '';
                    history.replaceState(null, '', url.toString());
                },
                (err: unknown) => {
                    if (id !== loadIdRef.current) return;
                    playgroundDispatch({ type: 'setInitialCompileDone' });
                    editorDispatch({
                        type: 'showNotification',
                        notification: {
                            message: `Failed to load example: ${err instanceof Error ? err.message : String(err)}`,
                            type: 'error',
                        },
                    });
                },
            );
        },
        [manifest, baseUrl, fs, editorDispatch, playgroundDispatch],
    );

    // The length of the last link Share made that was too long to hand out, or null
    // when no dialog about one is open.
    const [oversizedLinkLength, setOversizedLinkLength] = useState<
        number | null
    >(null);

    const handleShare = useCallback(async () => {
        try {
            const url = await capturePermalink(fs, window.location.href);
            if (url.length > MAX_PERMALINK_CHARS) {
                setOversizedLinkLength(url.length);
                return;
            }
            // The address bar shows the link too, for a user whose browser refuses
            // the clipboard write.
            history.replaceState(null, '', url);
            await navigator.clipboard.writeText(url);
            editorDispatch({
                type: 'showNotification',
                notification: {
                    message: 'Copied a link to this project.',
                    type: 'info',
                },
            });
        } catch (err) {
            console.error('Failed to share the project:', err);
            editorDispatch({
                type: 'showNotification',
                notification: {
                    message:
                        'Could not copy a link to this project. If the address bar shows a #project link, copy it from there.',
                    type: 'warning',
                },
            });
        }
    }, [fs, editorDispatch]);

    const rightPaneVisible = rightTabs.length > 0 && initialCompileDone;

    const rightPaneOverlay = !initialCompileDone ? (
        <div className={styles.compileSpinner}>
            <div className={styles.spinner} />
            <span>Compiling...</span>
        </div>
    ) : null;

    const banner =
        validationResult.mode === 'invalid' ? (
            <div className={styles.errorBanner}>
                {validationResult.errors.map((err, i) => (
                    <div key={`${i}-${err}`}>{err}</div>
                ))}
            </div>
        ) : null;

    // What the open bug report holds besides the user's text, or null while the report
    // is closed. It is read once, when the user opens the report, so the report
    // describes the moment the user chose to report.
    const [bugReport, setBugReport] = useState<{
        errors: readonly ShownError[];
        crashReport?: string;
    } | null>(null);
    const handleReportBug = useCallback(() => {
        const { leftTabs, rightTabs, lastCrashReport } =
            useEditorStore.getState();
        setBugReport({
            errors: shownErrors(
                [...leftTabs, ...rightTabs].map((tab) => tab.path),
            ),
            crashReport: lastCrashReport?.text,
        });
    }, []);
    const captureBugReportLink = useCallback(
        () => capturePermalink(fs, window.location.href),
        [fs],
    );

    const toolbar = (
        <>
            <Toolbar
                templates={templateItems}
                examples={exampleItems}
                onSelectTemplate={handleSelectTemplate}
                onSelectExample={handleSelectExample}
                onShare={() => void handleShare()}
                onReportBug={handleReportBug}
            />
            {oversizedLinkLength !== null && (
                <MessageDialog
                    title="Project too large to share"
                    message={oversizedLinkMessage(oversizedLinkLength)}
                    onClose={() => setOversizedLinkLength(null)}
                />
            )}
            {bugReport !== null && (
                <BugReportDialog
                    capturePermalink={captureBugReportLink}
                    errors={bugReport.errors}
                    crashReport={bugReport.crashReport}
                    environment={playgroundEnvironment()}
                    onClose={() => setBugReport(null)}
                />
            )}
        </>
    );

    return (
        <Editor
            fs={fs}
            isReadOnly={isBuildPath}
            rightPaneVisible={rightPaneVisible}
            rightPaneOverlay={rightPaneOverlay}
            banner={banner}
            toolbar={toolbar}
            statusBar={<StatusBar />}
        />
    );
};
