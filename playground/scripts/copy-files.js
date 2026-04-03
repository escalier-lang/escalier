import nodeFs from 'node:fs';
import nodePath from 'node:path';
import { fileURLToPath } from 'node:url';

/**
 * Recursively copy a directory and return the list of relative file paths.
 */
function copyDirRecursive(fs, src, dest) {
    const files = [];
    fs.mkdirSync(dest, { recursive: true });

    for (const entry of fs.readdirSync(src, { withFileTypes: true })) {
        const srcPath = nodePath.join(src, entry.name);
        const destPath = nodePath.join(dest, entry.name);

        if (entry.isDirectory()) {
            files.push(...copyDirRecursive(fs, srcPath, destPath));
        } else {
            fs.copyFileSync(srcPath, destPath);
            files.push(nodePath.relative(dest, destPath));
        }
    }

    return files;
}

/**
 * Recursively list all file paths in a directory, returned relative to `base`.
 */
function listFilesRecursive(fs, dir, base = dir) {
    const files = [];
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
        const fullPath = nodePath.join(dir, entry.name);
        if (entry.isDirectory()) {
            files.push(...listFilesRecursive(fs, fullPath, base));
        } else {
            files.push(nodePath.relative(base, fullPath));
        }
    }
    return files;
}

/**
 * Copy all subdirectories from `srcDir` into `public/${srcDir}` and return
 * a map of { name: relativeFilePaths[] } for each subdirectory.
 */
function copyCategory(fs, srcDir) {
    const result = {};
    if (!fs.existsSync(srcDir)) return result;
    fs.mkdirSync(nodePath.join('public', srcDir), { recursive: true });
    for (const name of fs.readdirSync(srcDir)) {
        const src = nodePath.join(srcDir, name);
        if (!fs.statSync(src).isDirectory()) continue;
        copyDirRecursive(fs, src, nodePath.join('public', srcDir, name));
        result[name] = listFilesRecursive(fs, src);
    }
    return result;
}

export function main(fs = nodeFs) {
    // --- Type definitions ---

    fs.mkdirSync('public/types', { recursive: true });

    fs.copyFileSync(
        'node_modules/typescript/lib/lib.es5.d.ts',
        'public/types/lib.es5.d.ts',
    );

    fs.copyFileSync(
        'node_modules/typescript/lib/lib.dom.d.ts',
        'public/types/lib.dom.d.ts',
    );

    const libDir = 'node_modules/typescript/lib';
    const es2015Files = fs
        .readdirSync(libDir)
        .filter((file) => file.startsWith('lib.es2015.'));
    for (const file of es2015Files) {
        fs.copyFileSync(`${libDir}/${file}`, `public/types/${file}`);
    }

    // --- Templates and examples ---

    const templates = copyCategory(fs, 'templates');
    const examples = copyCategory(fs, 'examples');

    // --- Write manifest ---

    const manifest = {
        types: ['lib.es5.d.ts', 'lib.dom.d.ts', ...es2015Files],
        templates,
        examples,
    };
    fs.writeFileSync(
        'public/types/manifest.json',
        JSON.stringify(manifest, null, 2),
    );

    return manifest;
}

// Run when executed directly (not imported)
if (fileURLToPath(import.meta.url) === process.argv[1]) {
    main();
}
