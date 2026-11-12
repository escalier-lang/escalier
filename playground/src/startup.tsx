import type { ReactNode } from 'react';

import { LoadingScreen, type LoadingStatus } from './components/loading-screen';
import {
    type Download,
    type DownloadProgress,
    downloadAll,
    frameThrottled,
} from './downloads';
import { type Manifest, type Volume, createVolume } from './fs/volume';

/** The files the playground needs before it can start the language server. */
export type StartupFiles = {
    /** The compiled language server. */
    wasm: ArrayBuffer;
    /** A volume that already holds the content of every standard library file. */
    volume: Volume;
};

/**
 * Downloads lsp-server.wasm from wasmUrl and every standard library file the manifest
 * lists, in parallel, and reports their progress to showStatus at most once per
 * animation frame. Once every download has arrived, it reports that the language
 * server is starting. Rejects when any download fails. showStatus receives no
 * progress after the returned promise settles.
 */
export async function downloadStartupFiles(
    manifest: Manifest,
    baseUrl: string,
    wasmUrl: string,
    showStatus: (status: LoadingStatus) => void,
): Promise<StartupFiles> {
    const builtins = manifest.builtins ?? [];
    const downloads: Download[] = [
        { url: wasmUrl, size: manifest.lspServerWasmSize },
        ...builtins.map((relPath) => ({
            url: `${baseUrl}escalier-builtins/${relPath}`,
            size: manifest.builtinSizes?.[relPath],
        })),
    ];
    const progress = frameThrottled((latest: DownloadProgress) =>
        showStatus({ kind: 'downloading', progress: latest }),
    );
    let contents: Uint8Array<ArrayBuffer>[];
    try {
        contents = await downloadAll(downloads, progress.call);
    } finally {
        // Stop before the caller shows its next status, so a frame scheduled by the
        // last chunk, or by a download still running after a failure, can't draw the
        // progress over it.
        progress.stop();
    }
    const [wasm, ...builtinContents] = contents;
    showStatus({ kind: 'starting' });

    const volume = createVolume(manifest, baseUrl);
    builtins.forEach((relPath, i) => {
        volume[`/node_modules/escalier-builtins/${relPath}`].content =
            builtinContents[i];
    });
    return { wasm: wasm.buffer, volume };
}

/**
 * Renders the loading screen, then runs start with a function that updates it.
 * start renders the playground itself once it's ready. When start throws, the
 * loading screen shows the error. Returns whether start succeeded, and logs
 * `App loaded` when it did.
 */
export async function runStartup(
    render: (node: ReactNode) => void,
    start: (showStatus: (status: LoadingStatus) => void) => Promise<void>,
): Promise<boolean> {
    const showStatus = (status: LoadingStatus) => {
        render(<LoadingScreen status={status} />);
    };
    showStatus({ kind: 'downloading', progress: { loaded: 0, total: 0 } });

    try {
        await start(showStatus);
    } catch (err) {
        console.error('Failed to load the playground:', err);
        showStatus({
            kind: 'failed',
            message: err instanceof Error ? err.message : String(err),
        });
        return false;
    }
    console.log('App loaded');
    return true;
}
