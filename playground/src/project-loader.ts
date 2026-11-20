import type { BrowserFS } from './fs/browser-fs';
import type { Manifest } from './fs/volume';
import { useProjectDiagnosticsStore } from './project-diagnostics-store';

export type ProjectKind = 'template' | 'example';

/**
 * Reports whether path is compiler output, which lives under `/build/` or under a
 * package's own `build/` directory.
 */
export function isBuildPath(path: string): boolean {
    return (
        path.startsWith('/build/') || /^\/packages\/[^/]+\/build\//.test(path)
    );
}

/**
 * Load a template or example project into the virtual filesystem.
 *
 * 1. Fetches file contents from the public directory.
 * 2. Replaces the project in the filesystem with them (see writeProjectFiles).
 * 3. Returns the primary file to open (defaults to lib/index.esc).
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

    if (!fileList || fileList.length === 0) {
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

    writeProjectFiles(fs, fetched);

    // Determine the primary file to open
    const hasLibIndex = fileList.includes('lib/index.esc');
    const firstEsc = fileList.find((f) => f.endsWith('.esc'));
    return hasLibIndex
        ? '/lib/index.esc'
        : firstEsc
          ? `/${firstEsc}`
          : `/${fileList[0]}`;
}

/**
 * Replaces the project in the filesystem with files. It clears everything outside
 * node_modules and forgets the diagnostics recorded for the old files, then writes each
 * file, creating its parent directories. BrowserFS
 * emits an FS event for each write, which the main.tsx listener forwards to the LSP
 * as workspaceDidChangeWatchedFiles.
 */
export function writeProjectFiles(
    fs: BrowserFS,
    files: readonly { path: string; content: string }[],
): void {
    fs.clear();
    // fs.clear emits no delete events, and the language server never clears the
    // diagnostics of a file it no longer sees, so the old project's diagnostics would stay
    // in the count.
    useProjectDiagnosticsStore.getState().dispatch({ type: 'forgetAll' });

    const encoder = new TextEncoder();

    for (const { path, content } of files) {
        // Ensure parent directories exist
        const parts = path.split('/').filter((p) => p.length > 0);
        for (let i = 1; i < parts.length; i++) {
            const dirPath = `/${parts.slice(0, i).join('/')}`;
            fs.mkdir(dirPath, (err) => {
                // Ignore EEXIST errors — the directory may already exist
                if (err && (err as NodeJS.ErrnoException).code !== 'EEXIST') {
                    console.error(
                        `Failed to create directory ${dirPath}:`,
                        err,
                    );
                }
            });
        }

        fs.writeFile(path, encoder.encode(content), (err) => {
            if (err) {
                console.error(`Failed to write ${path}:`, err);
            }
        });
    }
}
