import { afterEach, describe, expect, test, vi } from 'vitest';

import { type DownloadProgress, downloadAll, fetchBytes } from './downloads';

/** Returns a Response whose body streams chunks one at a time. */
function streamingResponse(chunks: number[][]): Response {
    const body = new ReadableStream<Uint8Array>({
        start(controller) {
            for (const chunk of chunks) {
                controller.enqueue(new Uint8Array(chunk));
            }
            controller.close();
        },
    });
    return new Response(body);
}

/** Stubs fetch to answer each url with the chunks bodies names for it. */
function stubFetch(bodies: Record<string, number[][]>) {
    vi.stubGlobal(
        'fetch',
        vi.fn(async (url: string) =>
            url in bodies
                ? streamingResponse(bodies[url])
                : new Response(null, { status: 404, statusText: 'Not Found' }),
        ),
    );
}

afterEach(() => {
    vi.unstubAllGlobals();
});

describe('fetchBytes', () => {
    test('joins the chunks and reports the size of each one', async () => {
        stubFetch({ '/a': [[1, 2], [3], [4, 5, 6]] });
        const counts: number[] = [];

        const bytes = await fetchBytes('/a', (count) => counts.push(count));

        expect(bytes).toEqual(new Uint8Array([1, 2, 3, 4, 5, 6]));
        expect(counts).toEqual([2, 1, 3]);
    });

    test('reads the whole body when the response has no stream', async () => {
        vi.stubGlobal(
            'fetch',
            vi.fn(async () => ({
                ok: true,
                body: null,
                arrayBuffer: async () => new Uint8Array([7, 8]).buffer,
            })),
        );
        const counts: number[] = [];

        const bytes = await fetchBytes('/a', (count) => counts.push(count));

        expect(bytes).toEqual(new Uint8Array([7, 8]));
        expect(counts).toEqual([2]);
    });

    test('throws when the server answers with an error status', async () => {
        stubFetch({});

        await expect(fetchBytes('/missing', () => {})).rejects.toThrow(
            'Failed to fetch /missing: Not Found',
        );
    });
});

describe('downloadAll', () => {
    test('returns the bodies in order and reports bytes against the total', async () => {
        stubFetch({ '/a': [[1, 2], [3]], '/b': [[4]] });
        const reports: DownloadProgress[] = [];

        const bodies = await downloadAll(
            [
                { url: '/a', size: 3 },
                { url: '/b', size: 1 },
            ],
            (progress) => reports.push(progress),
        );

        expect(bodies).toEqual([
            new Uint8Array([1, 2, 3]),
            new Uint8Array([4]),
        ]);
        expect(reports[0]).toEqual({ loaded: 0, total: 4 });
        expect(reports.at(-1)).toEqual({ loaded: 4, total: 4 });
        expect(reports.map((r) => r.loaded)).toEqual(
            [...reports.map((r) => r.loaded)].sort((x, y) => x - y),
        );
    });

    test('counts a download that outgrows its size as its size', async () => {
        stubFetch({
            '/a': [
                [1, 2],
                [3, 4],
            ],
        });
        const reports: DownloadProgress[] = [];

        await downloadAll([{ url: '/a', size: 3 }], (progress) =>
            reports.push(progress),
        );

        expect(reports).toEqual([
            { loaded: 0, total: 3 },
            { loaded: 2, total: 3 },
            { loaded: 3, total: 3 },
        ]);
    });

    test('leaves a download without a size out of the progress', async () => {
        stubFetch({ '/a': [[1, 2]], '/b': [[3, 4, 5]] });
        const reports: DownloadProgress[] = [];

        const bodies = await downloadAll(
            [{ url: '/a', size: 2 }, { url: '/b' }],
            (progress) => reports.push(progress),
        );

        expect(bodies[1]).toEqual(new Uint8Array([3, 4, 5]));
        expect(reports).toEqual([
            { loaded: 0, total: 2 },
            { loaded: 2, total: 2 },
        ]);
    });

    test('rejects when any download fails', async () => {
        stubFetch({ '/a': [[1]] });

        await expect(
            downloadAll(
                [
                    { url: '/a', size: 1 },
                    { url: '/missing', size: 1 },
                ],
                () => {},
            ),
        ).rejects.toThrow('Failed to fetch /missing: Not Found');
    });
});
