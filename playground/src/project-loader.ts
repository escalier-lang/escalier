import type { BrowserFS } from './fs/browser-fs';
import type { Manifest } from './fs/volume';

export type ProjectKind = 'template' | 'example';

/**
 * Load a template or example project into the virtual filesystem.
 *
 * 1. Fetches file contents from the public directory.
 * 2. Clears the filesystem (preserving node_modules/).
 * 3. Writes the fetched files (BrowserFS emits FS events, which the
 *    main.tsx listener forwards to the LSP as workspaceDidChangeWatchedFiles).
 * 4. Returns the primary file to open (defaults to lib/index.esc).
 */
export async function loadProject(
    slug: string,
    kind: ProjectKind,
    manifest: Manifest,
    baseUrl: string,
    fs: BrowserFS,
): Promise<string> {
    const fileList =
        kind === 'template'
            ? manifest.templates[slug]
            : manifest.examples[slug];

    if (!fileList) {
        throw new Error(`Unknown ${kind}: ${slug}`);
    }

    // Fetch all file contents in parallel
    const fetched = await Promise.all(
        fileList.map(async (relativePath) => {
            const url = `${baseUrl}${kind}s/${slug}/${relativePath}`;
            const response = await fetch(url);
            if (!response.ok) {
                throw new Error(
                    `Failed to fetch ${url}: ${response.statusText}`,
                );
            }
            const text = await response.text();
            return { path: `/${relativePath}`, content: text };
        }),
    );

    // Clear the filesystem (preserves node_modules/)
    fs.clear();

    // Write all files to the filesystem
    const encoder = new TextEncoder();

    for (const { path, content } of fetched) {
        // Ensure parent directories exist
        const parts = path.split('/').filter((p) => p.length > 0);
        for (let i = 1; i < parts.length; i++) {
            const dirPath = `/${parts.slice(0, i).join('/')}`;
            fs.mkdir(dirPath, (err) => {
                // Ignore EEXIST errors — the directory may already exist
                if (err && (err as NodeJS.ErrnoException).code !== 'EEXIST') {
                    console.error(`Failed to create directory ${dirPath}:`, err);
                }
            });
        }

        fs.writeFile(path, encoder.encode(content), (err) => {
            if (err) {
                console.error(`Failed to write ${path}:`, err);
            }
        });
    }

    // Determine the primary file to open
    const hasLibIndex = fileList.includes('lib/index.esc');
    const firstEsc = fileList.find((f) => f.endsWith('.esc'));
    return hasLibIndex
        ? '/lib/index.esc'
        : firstEsc
          ? `/${firstEsc}`
          : `/${fileList[0]}`;
}
