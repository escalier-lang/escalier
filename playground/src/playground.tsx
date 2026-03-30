import { useCallback, useEffect } from 'react';

import { Editor } from './editor';
import type { BrowserFS } from './fs/browser-fs';
import { usePlaygroundDispatch, usePlaygroundState } from './playground-state';

import styles from './playground.module.css';

type PlaygroundProps = {
    fs: BrowserFS;
    editorState: import('./editor-state').EditorState;
    editorDispatch: import('react').Dispatch<
        import('./editor-state').EditorAction
    >;
};

export const Playground = ({
    fs,
    editorState,
    editorDispatch,
}: PlaygroundProps) => {
    const { initialCompileDone, validationResult } = usePlaygroundState();
    const playgroundDispatch = usePlaygroundDispatch();

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

    return (
        <Editor
            fs={fs}
            state={editorState}
            dispatch={editorDispatch}
            isReadOnly={isReadOnly}
            rightPaneVisible={rightPaneVisible}
            rightPaneOverlay={rightPaneOverlay}
            banner={banner}
        />
    );
};
