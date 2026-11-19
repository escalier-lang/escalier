import type { TabLayout } from './editor-state';
import type { FSDir } from './fs/fs-node';
import { isBuildPath } from './project-loader';

/**
 * The state a permalink recreates: the project's source files and the tabs open on
 * each side of the editor. Build output is left out because the compiler writes it
 * again, but open build files stay in the tab layouts.
 */
export type PlaygroundSnapshot = {
    /** The text of each project file, keyed by absolute path. */
    files: Record<string, string>;
    left: TabLayout;
    right: TabLayout;
};

/** The version written into each permalink, so a later format can tell old links apart. */
const PERMALINK_VERSION = 1;

/**
 * The longest permalink the playground hands out. It is about half of the 65,536
 * characters GitHub holds in an issue body, which leaves the rest for a description.
 */
export const MAX_PERMALINK_CHARS = 32_000;

/** The URL hash parameter that carries an encoded snapshot. */
export const PERMALINK_PARAM = 'project';

/**
 * Returns the text of every project file under root, keyed by absolute path. It skips
 * node_modules and build output. currentText returns the text to record for a path,
 * or undefined to record the file's content.
 */
export function collectProjectFiles(
    root: FSDir,
    currentText: (path: string) => string | undefined,
): Record<string, string> {
    const decoder = new TextDecoder();
    const files: Record<string, string> = {};
    const visit = (dir: FSDir, dirPath: string) => {
        for (const [name, node] of dir.children) {
            const path = `${dirPath}/${name}`;
            if (path === '/node_modules' || isBuildPath(`${path}/`)) {
                continue;
            }
            if (node.type === 'dir') {
                visit(node, path);
            } else if (node.type === 'file') {
                files[path] = currentText(path) ?? decoder.decode(node.content);
            }
        }
    };
    visit(root, '');
    return files;
}

/** Returns snapshot as compressed, URL-safe text for a permalink. */
export async function encodeSnapshot(
    snapshot: PlaygroundSnapshot,
): Promise<string> {
    const json = JSON.stringify({ v: PERMALINK_VERSION, ...snapshot });
    const compressed = await transform(
        new TextEncoder().encode(json),
        new CompressionStream('deflate-raw'),
    );
    return toBase64Url(compressed);
}

/**
 * Returns the snapshot that encodeSnapshot encoded as text. Throws an Error whose
 * message says what is wrong when text is not a valid snapshot.
 */
export async function decodeSnapshot(
    text: string,
): Promise<PlaygroundSnapshot> {
    let value: unknown;
    try {
        const json = await transform(
            fromBase64Url(text),
            new DecompressionStream('deflate-raw'),
        );
        value = JSON.parse(new TextDecoder().decode(json));
    } catch {
        throw new Error('The link is damaged or incomplete.');
    }
    return validateSnapshot(value);
}

/**
 * Returns the encoded snapshot in a URL hash such as `#project=...`, or null when the
 * hash has none.
 */
export function snapshotParam(hash: string): string | null {
    return new URLSearchParams(hash.replace(/^#/, '')).get(PERMALINK_PARAM);
}

/** Returns href with its query replaced by a hash that carries encoded. */
export function permalinkUrl(href: string, encoded: string): string {
    const url = new URL(href);
    url.search = '';
    url.hash = `${PERMALINK_PARAM}=${encoded}`;
    return url.toString();
}

function validateSnapshot(value: unknown): PlaygroundSnapshot {
    if (!isRecord(value)) {
        throw new Error('The link holds no project.');
    }
    if (value.v !== PERMALINK_VERSION) {
        throw new Error(
            `The link uses format version ${String(value.v)}, which this playground does not read.`,
        );
    }
    const { files } = value;
    if (!isRecord(files)) {
        throw new Error('The link holds no project files.');
    }
    for (const [path, content] of Object.entries(files)) {
        if (!isProjectPath(path)) {
            throw new Error(
                `The link holds a file at ${path}, outside the project.`,
            );
        }
        if (typeof content !== 'string') {
            throw new Error(`The link holds no text for ${path}.`);
        }
    }
    return {
        files: files as Record<string, string>,
        left: validateLayout(value.left, 'left'),
        right: validateLayout(value.right, 'right'),
    };
}

function validateLayout(value: unknown, side: string): TabLayout {
    if (
        !isRecord(value) ||
        !Array.isArray(value.paths) ||
        !value.paths.every((path) => typeof path === 'string')
    ) {
        throw new Error(`The link holds no tabs for the ${side} side.`);
    }
    const { paths, active } = value;
    for (const path of paths) {
        // A tab may show build output, which the snapshot's files leave out.
        if (!isAbsolutePath(path) || isInNodeModules(path)) {
            throw new Error(
                `The link opens a tab for ${path}, outside the project.`,
            );
        }
    }
    if (new Set(paths).size !== paths.length) {
        throw new Error(
            `The link opens the same file twice on the ${side} side.`,
        );
    }
    const activeIsValid =
        active === null
            ? paths.length === 0
            : Number.isInteger(active) &&
              (active as number) >= 0 &&
              (active as number) < paths.length;
    if (!activeIsValid) {
        throw new Error(`The link names no open tab on the ${side} side.`);
    }
    return { paths, active: active as number | null };
}

/**
 * Reports whether path is a file a snapshot may write: an absolute path outside
 * node_modules, which holds the standard library, and outside build output.
 */
function isProjectPath(path: string): boolean {
    return isAbsolutePath(path) && !isInNodeModules(path) && !isBuildPath(path);
}

/** Reports whether path starts with `/` and has no empty, `.` or `..` segment. */
function isAbsolutePath(path: string): boolean {
    return (
        path.startsWith('/') &&
        path
            .slice(1)
            .split('/')
            .every(
                (segment) =>
                    segment !== '' && segment !== '.' && segment !== '..',
            )
    );
}

function isInNodeModules(path: string): boolean {
    return path === '/node_modules' || path.startsWith('/node_modules/');
}

function isRecord(value: unknown): value is Record<string, unknown> {
    return typeof value === 'object' && value !== null && !Array.isArray(value);
}

async function transform(
    bytes: Uint8Array<ArrayBuffer>,
    stream: CompressionStream | DecompressionStream,
): Promise<Uint8Array<ArrayBuffer>> {
    const output = new Blob([bytes]).stream().pipeThrough(stream);
    return new Uint8Array(await new Response(output).arrayBuffer());
}

function toBase64Url(bytes: Uint8Array): string {
    let binary = '';
    for (const byte of bytes) {
        binary += String.fromCharCode(byte);
    }
    return btoa(binary)
        .replace(/\+/g, '-')
        .replace(/\//g, '_')
        .replace(/=+$/, '');
}

function fromBase64Url(text: string): Uint8Array<ArrayBuffer> {
    const binary = atob(text.replace(/-/g, '+').replace(/_/g, '/'));
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) {
        bytes[i] = binary.charCodeAt(i);
    }
    return bytes;
}
