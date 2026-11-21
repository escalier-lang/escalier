import { useEditorStore } from '#editor/editor-store';
import type { BrowserFS } from '#fs/browser-fs';
import type { Manifest } from '#fs/volume';

import { decodeSnapshot, snapshotParam } from './permalink';
import { loadProject, writeProjectFiles } from './project-loader';

/**
 * Opens the project the page's URL names: the permalink snapshot in its hash, or
 * else the example in its query (see openExample). Returns true when the tabs came
 * from a snapshot.
 */
export async function openInitialProject(
    manifest: Manifest,
    baseUrl: string,
    fs: BrowserFS,
): Promise<boolean> {
    const encoded = snapshotParam(window.location.hash);
    if (encoded !== null && (await openSnapshot(encoded, fs))) {
        return true;
    }
    await openExample(manifest, baseUrl, fs);
    return false;
}

/** Reloads win's page whenever its hash changes to one that holds a permalink. */
export function reloadOnPastedPermalink(
    win: Pick<Window, 'addEventListener' | 'location'>,
): void {
    // A link pasted into the address bar of an open playground changes only the
    // hash, which loads nothing, so the page reloads to open it.
    win.addEventListener('hashchange', () => {
        if (snapshotParam(win.location.hash) !== null) {
            win.location.reload();
        }
    });
}

/**
 * Writes the project in an encoded permalink snapshot to fs and opens its tabs.
 * Returns false, after telling the user why, when the snapshot can't be read.
 */
export async function openSnapshot(
    encoded: string,
    fs: BrowserFS,
): Promise<boolean> {
    const { dispatch } = useEditorStore.getState();
    try {
        const snapshot = await decodeSnapshot(encoded);
        writeProjectFiles(
            fs,
            Object.entries(snapshot.files).map(([path, content]) => ({
                path,
                content,
            })),
        );
        dispatch({
            type: 'restoreTabs',
            left: snapshot.left,
            right: snapshot.right,
        });
        return true;
    } catch (err) {
        console.error('Failed to open the permalink:', err);
        // Drop the link from the address bar so a reload doesn't repeat the error.
        const url = new URL(window.location.href);
        url.hash = '';
        history.replaceState(null, '', url.toString());
        dispatch({
            type: 'showNotification',
            notification: {
                message: `Could not open the project in this link. ${err instanceof Error ? err.message : String(err)} Loading an example instead.`,
                type: 'error',
            },
        });
        return false;
    }
}

/**
 * Loads the example the `example` query parameter names into fs, or Hello World when
 * it names none, and opens its primary file.
 */
export async function openExample(
    manifest: Manifest,
    baseUrl: string,
    fs: BrowserFS,
) {
    const params = new URLSearchParams(window.location.search);
    const exampleParam = params.get('example');

    let slug = 'hello-world';
    const kind: 'example' | 'template' = 'example';

    if (exampleParam && manifest.examples[exampleParam]) {
        slug = exampleParam;
    } else if (exampleParam) {
        console.warn(
            `Unknown example "${exampleParam}", falling back to hello-world`,
        );
        useEditorStore.getState().dispatch({
            type: 'showNotification',
            notification: {
                message: `Unknown example "${exampleParam}". Loading Hello World instead.`,
                type: 'warning',
            },
        });
        // Remove the invalid query param so the warning doesn't repeat on refresh
        const url = new URL(window.location.href);
        url.searchParams.set('example', slug);
        history.replaceState(null, '', url.toString());
    }

    try {
        const primaryFile = await loadProject(
            slug,
            kind,
            manifest,
            baseUrl,
            fs,
        );
        useEditorStore.getState().dispatch({
            type: 'resetTabs',
            primaryFile,
        });
    } catch (err) {
        console.error('Failed to load initial project:', err);
        useEditorStore.getState().dispatch({
            type: 'showNotification',
            notification: {
                message: `Failed to load initial project: ${err instanceof Error ? err.message : String(err)}`,
                type: 'error',
            },
        });
    }
}
