/** A file to download, with its size in bytes when the manifest records it. */
export type Download = {
    url: string;
    size?: number;
};

/** How many bytes of the downloads with a known size have arrived. */
export type DownloadProgress = {
    loaded: number;
    total: number;
};

/**
 * Fetches url and returns its body. onBytes receives the number of bytes in each
 * chunk as it arrives. Throws when the server answers with an error status.
 */
export async function fetchBytes(
    url: string,
    onBytes: (count: number) => void,
): Promise<Uint8Array<ArrayBuffer>> {
    const response = await fetch(url);
    if (!response.ok) {
        throw new Error(`Failed to fetch ${url}: ${response.statusText}`);
    }
    if (!response.body) {
        const bytes = new Uint8Array(await response.arrayBuffer());
        onBytes(bytes.length);
        return bytes;
    }
    const reader = response.body.getReader();
    const chunks: Uint8Array[] = [];
    let length = 0;
    for (;;) {
        const { done, value } = await reader.read();
        if (done) {
            break;
        }
        chunks.push(value);
        length += value.length;
        onBytes(value.length);
    }
    const bytes = new Uint8Array(length);
    let offset = 0;
    for (const chunk of chunks) {
        bytes.set(chunk, offset);
        offset += chunk.length;
    }
    return bytes;
}

/**
 * Fetches every download in parallel and returns their bodies in the same order.
 * onProgress receives the bytes received so far against the sum of the known sizes.
 * A download without a size counts toward neither number, and a download that
 * delivers more bytes than its size counts as its size, so loaded never exceeds
 * total.
 */
export function downloadAll(
    downloads: readonly Download[],
    onProgress: (progress: DownloadProgress) => void,
): Promise<Uint8Array<ArrayBuffer>[]> {
    const total = downloads.reduce((sum, d) => sum + (d.size ?? 0), 0);
    const received = downloads.map(() => 0);
    let loaded = 0;
    onProgress({ loaded, total });
    return Promise.all(
        downloads.map((download, i) =>
            fetchBytes(download.url, (count) => {
                if (download.size === undefined) {
                    return;
                }
                const next = Math.min(received[i] + count, download.size);
                loaded += next - received[i];
                received[i] = next;
                onProgress({ loaded, total });
            }),
        ),
    );
}
