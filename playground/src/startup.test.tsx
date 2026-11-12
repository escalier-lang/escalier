import { type ReactNode, isValidElement } from 'react';
import { afterEach, describe, expect, test, vi } from 'vitest';

import type { LoadingStatus } from './components/loading-screen';
import type { Manifest } from './fs/volume';
import { downloadStartupFiles, runStartup } from './startup';

const manifest: Manifest = {
    types: ['lib.es5.d.ts'],
    templates: {},
    examples: {},
    builtins: ['std/prelude.esc', 'std/math.esc'],
    builtinSizes: { 'std/prelude.esc': 3, 'std/math.esc': 2 },
    lspServerWasmSize: 4,
};

const bodies: Record<string, number[]> = {
    '/lsp-server.wasm': [0, 97, 115, 109],
    '/base/escalier-builtins/std/prelude.esc': [1, 2, 3],
    '/base/escalier-builtins/std/math.esc': [4, 5],
};

/** Stubs fetch to answer from bodies, and with 404 for any other url. */
function stubFetch() {
    vi.stubGlobal(
        'fetch',
        vi.fn(async (url: string) =>
            url in bodies
                ? new Response(new Uint8Array(bodies[url]))
                : new Response(null, { status: 404, statusText: 'Not Found' }),
        ),
    );
}

/** Stubs requestAnimationFrame and returns a function that runs the queued frames. */
function stubFrames(): () => void {
    const frames = new Map<number, FrameRequestCallback>();
    let nextId = 1;
    vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => {
        frames.set(nextId, cb);
        return nextId++;
    });
    vi.stubGlobal('cancelAnimationFrame', (id: number) => {
        frames.delete(id);
    });
    return () => {
        const queued = [...frames.values()];
        frames.clear();
        for (const cb of queued) {
            cb(0);
        }
    };
}

/** Returns the status prop of each LoadingScreen element render received. */
function renderedStatuses(render: ReturnType<typeof vi.fn>): LoadingStatus[] {
    return render.mock.calls.map((call) => {
        const node: ReactNode = call[0];
        if (!isValidElement<{ status: LoadingStatus }>(node)) {
            throw new Error('render received something other than an element');
        }
        return node.props.status;
    });
}

afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
});

describe('downloadStartupFiles', () => {
    test('puts each standard library file into the volume', async () => {
        stubFetch();
        stubFrames();

        const { wasm, volume } = await downloadStartupFiles(
            manifest,
            '/base/',
            '/lsp-server.wasm',
            () => {},
        );

        expect(new Uint8Array(wasm)).toEqual(new Uint8Array([0, 97, 115, 109]));
        expect(
            volume['/node_modules/escalier-builtins/std/prelude.esc'],
        ).toEqual({
            url: '/base/escalier-builtins/std/prelude.esc',
            content: new Uint8Array([1, 2, 3]),
        });
        expect(
            volume['/node_modules/escalier-builtins/std/math.esc'].content,
        ).toEqual(new Uint8Array([4, 5]));
        // Files the language server never reads keep loading on demand.
        expect(volume['/node_modules/typescript/lib/lib.es5.d.ts']).toEqual({
            url: '/base/types/lib.es5.d.ts',
            content: null,
        });
    });

    test('reports starting once the downloads arrive, and no progress after', async () => {
        stubFetch();
        const runFrames = stubFrames();
        const showStatus = vi.fn();

        await downloadStartupFiles(
            manifest,
            '/base/',
            '/lsp-server.wasm',
            showStatus,
        );
        runFrames();

        expect(showStatus.mock.calls).toEqual([[{ kind: 'starting' }]]);
    });

    test('reports the latest progress once per frame', async () => {
        const runFrames = stubFrames();
        let release: (() => void) | undefined;
        vi.stubGlobal(
            'fetch',
            vi.fn(async (url: string) => {
                if (url === '/lsp-server.wasm') {
                    // Hold the wasm back so the frame runs while downloads continue.
                    await new Promise<void>((resolve) => {
                        release = resolve;
                    });
                }
                return new Response(new Uint8Array(bodies[url]));
            }),
        );
        const showStatus = vi.fn();

        const files = downloadStartupFiles(
            manifest,
            '/base/',
            '/lsp-server.wasm',
            showStatus,
        );
        // Wait until the wasm request is held and the other downloads have finished.
        await vi.waitFor(() => expect(release).toBeDefined());
        await new Promise((resolve) => setTimeout(resolve, 0));
        runFrames();
        release?.();
        await files;

        expect(showStatus.mock.calls).toEqual([
            [{ kind: 'downloading', progress: { loaded: 5, total: 9 } }],
            [{ kind: 'starting' }],
        ]);
    });

    test('rejects when a download fails', async () => {
        stubFetch();
        stubFrames();

        await expect(
            downloadStartupFiles(
                { ...manifest, builtins: ['std/missing.esc'] },
                '/base/',
                '/lsp-server.wasm',
                () => {},
            ),
        ).rejects.toThrow(
            'Failed to fetch /base/escalier-builtins/std/missing.esc: Not Found',
        );
    });
});

describe('runStartup', () => {
    test('shows the loading screen and lets start update it', async () => {
        const log = vi.spyOn(console, 'log').mockImplementation(() => {});
        const render = vi.fn();

        const started = await runStartup(render, async (showStatus) => {
            showStatus({ kind: 'starting' });
        });

        expect(started).toBe(true);
        expect(renderedStatuses(render)).toEqual([
            { kind: 'downloading', progress: { loaded: 0, total: 0 } },
            { kind: 'starting' },
        ]);
        expect(log).toHaveBeenCalledWith('App loaded');
    });

    test('shows the error when start throws', async () => {
        vi.spyOn(console, 'error').mockImplementation(() => {});
        const render = vi.fn();

        const started = await runStartup(render, async () => {
            throw new Error('Failed to fetch /lsp-server.wasm: Not Found');
        });

        expect(started).toBe(false);
        expect(renderedStatuses(render).at(-1)).toEqual({
            kind: 'failed',
            message: 'Failed to fetch /lsp-server.wasm: Not Found',
        });
    });

    test('shows a thrown value that is not an Error', async () => {
        vi.spyOn(console, 'error').mockImplementation(() => {});
        const render = vi.fn();

        await runStartup(render, async () => {
            throw 'offline';
        });

        expect(renderedStatuses(render).at(-1)).toEqual({
            kind: 'failed',
            message: 'offline',
        });
    });
});
