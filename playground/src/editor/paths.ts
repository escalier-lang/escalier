import { diagnosticsLanguageID } from '../diagnostics/diagnostics-language';
import { DIAGNOSTICS_TAB_PATH } from '../diagnostics/project-diagnostics';
import type { BrowserFS } from '../fs/browser-fs';
import { languageID } from '../language/language';
import { collectProjectFiles } from '../project/permalink';

/** Returns the file name at the end of path. */
export function displayName(path: string): string {
    return path.split('/').pop() ?? path;
}

/** Returns the Monaco language id for a file path. */
export function languageForPath(path: string): string {
    if (path === DIAGNOSTICS_TAB_PATH) return diagnosticsLanguageID;
    if (path.endsWith('.esc')) return languageID;
    if (path.endsWith('.js')) return 'javascript';
    if (path.endsWith('.ts') || path.endsWith('.d.ts')) return 'typescript';
    if (path.endsWith('.json') || path.endsWith('.js.map')) return 'json';
    if (path.endsWith('.toml')) return 'toml';
    return 'plaintext';
}

/** Returns the Monaco URI string for a filesystem path. */
export function pathToUri(path: string): string {
    return `file://${path}`;
}

/**
 * Returns the path of the project's first Escalier file in path order, preferring one under
 * `/lib/`, or null when the project has none.
 */
export function firstEscalierFile(fs: BrowserFS): string | null {
    const paths = Object.keys(collectProjectFiles(fs.rootDir, () => ''))
        .filter((path) => path.endsWith('.esc'))
        .sort();
    return paths.find((path) => path.startsWith('/lib/')) ?? paths[0] ?? null;
}

/**
 * Load file content from BrowserFS. BrowserFS callbacks fire synchronously
 * (it's all in-memory), so we can collect the result in a closure.
 * Returns null if the file doesn't exist.
 */
export function loadFileContent(fs: BrowserFS, path: string): string | null {
    let result: string | null = null;

    let called = false;
    fs.open(path, 'r', undefined, (openErr, fd) => {
        called = true;
        if (openErr || fd === undefined) return;

        fs.fstat(fd, (statErr, stats) => {
            if (statErr) {
                fs.close(fd, () => {});
                return;
            }

            const buf = new Uint8Array(stats.size);
            fs.read(fd, buf, 0, stats.size, 0, (readErr) => {
                fs.close(fd, () => {});
                if (readErr) return;
                result = new TextDecoder().decode(buf);
            });
        });
    });

    if (!called) {
        throw new Error(
            'BrowserFS callback was not synchronous — loadFileContent requires synchronous callbacks',
        );
    }

    return result;
}
