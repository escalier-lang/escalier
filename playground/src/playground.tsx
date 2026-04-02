import { useCallback, useEffect, useMemo, useRef } from 'react';

import { Toolbar } from './components/toolbar';
import { Editor } from './editor';
import { useEditorStore } from './editor-store';
import type { BrowserFS } from './fs/browser-fs';
import type { Manifest } from './fs/volume';
import { usePlaygroundStore } from './playground-store';
import { loadProject } from './project-loader';

import styles from './playground.module.css';

function slugToLabel(slug: string): string {
    return slug
        .split('-')
        .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
        .join(' ');
}

type PlaygroundProps = {
    fs: BrowserFS;
    manifest: Manifest;
    baseUrl: string;
};

export const Playground = ({ fs, manifest, baseUrl }: PlaygroundProps) => {
    const { dispatch: editorDispatch, ...editorState } = useEditorStore();
    const { dispatch: playgroundDispatch, ...playgroundState } =
        usePlaygroundStore();
    const { initialCompileDone, validationResult } = playgroundState;

    const { leftTabs, rightTabs } = editorState;

    const isReadOnly = useCallback(
        (path: string) =>
            path.startsWith('/build/') ||
            /^\/packages\/[^/]+\/build\//.test(path),
        [],
    );

    // After initial compilation, open the build output files as right tabs.
    // Only auto-open build files whose source is currently open on the left.
    useEffect(() => {
        if (initialCompileDone) return;

        // Build a set of expected output prefixes from the open left-side
        // .esc files. The compiler maps all outputs under /build/ at the root:
        //   "/bin/main.esc" → "/build/bin/main."
        //   "/packages/foo/lib/bar.esc" → "/build/packages/foo/lib/bar."
        const buildPrefixes = new Set<string>();
        for (const tab of leftTabs) {
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
                editorDispatch({
                    type: 'openFile',
                    path: event.path,
                    side: 'right',
                });
            }

            // Mark compilation done after the first batch of build files.
            // queueMicrotask ensures all synchronous FS writes from one
            // compilation are captured before we mark done.
            if (!markedDone) {
                markedDone = true;
                queueMicrotask(() => {
                    playgroundDispatch({ type: 'setInitialCompileDone' });
                });
            }
        };

        fs.events.on(listener);
        return () => fs.events.off(listener);
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
            loadProject(slug, 'template', manifest, baseUrl, fs).then(
                (primaryFile) => {
                    if (id !== loadIdRef.current) return;
                    editorDispatch({ type: 'resetTabs', primaryFile });
                    // Clear the query param when switching to a template
                    history.replaceState(null, '', window.location.pathname);
                },
                (err) => {
                    if (id !== loadIdRef.current) return;
                    editorDispatch({
                        type: 'showNotification',
                        notification: {
                            message: `Failed to load template: ${err.message}`,
                            type: 'error',
                        },
                    });
                },
            );
        },
        [manifest, baseUrl, fs, editorDispatch],
    );

    const handleSelectExample = useCallback(
        (slug: string) => {
            const id = ++loadIdRef.current;
            loadProject(slug, 'example', manifest, baseUrl, fs).then(
                (primaryFile) => {
                    if (id !== loadIdRef.current) return;
                    editorDispatch({ type: 'resetTabs', primaryFile });
                    // Update URL query param for deep linking
                    const url = new URL(window.location.href);
                    url.searchParams.set('example', slug);
                    history.replaceState(null, '', url.toString());
                },
                (err) => {
                    if (id !== loadIdRef.current) return;
                    editorDispatch({
                        type: 'showNotification',
                        notification: {
                            message: `Failed to load example: ${err.message}`,
                            type: 'error',
                        },
                    });
                },
            );
        },
        [manifest, baseUrl, fs, editorDispatch],
    );

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

    const toolbar = (
        <Toolbar
            templates={templateItems}
            examples={exampleItems}
            onSelectTemplate={handleSelectTemplate}
            onSelectExample={handleSelectExample}
        />
    );

    return (
        <Editor
            fs={fs}
            isReadOnly={isReadOnly}
            rightPaneVisible={rightPaneVisible}
            rightPaneOverlay={rightPaneOverlay}
            banner={banner}
            toolbar={toolbar}
        />
    );
};
