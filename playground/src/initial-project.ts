import { useEditorStore } from './editor-store';
import type { BrowserFS } from './fs/browser-fs';
import type { Manifest } from './fs/volume';
import { loadProject } from './project-loader';

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
                message: `Failed to load initial project: ${err instanceof Error ? err.message : err}`,
                type: 'error',
            },
        });
    }
}
