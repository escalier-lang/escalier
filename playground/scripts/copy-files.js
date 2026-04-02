import fs from 'node:fs';
import path from 'node:path';

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

/**
 * Recursively copy a directory and return the list of relative file paths.
 */
function copyDirRecursive(src, dest) {
    const files = [];
    fs.mkdirSync(dest, { recursive: true });

    for (const entry of fs.readdirSync(src, { withFileTypes: true })) {
        const srcPath = path.join(src, entry.name);
        const destPath = path.join(dest, entry.name);

        if (entry.isDirectory()) {
            files.push(...copyDirRecursive(srcPath, destPath));
        } else {
            fs.copyFileSync(srcPath, destPath);
            files.push(path.relative(dest, destPath));
        }
    }

    return files;
}

/**
 * Walk a directory and return relative file paths (without copying).
 */
function walkDir(dir, base = dir) {
    const files = [];
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
        const fullPath = path.join(dir, entry.name);
        if (entry.isDirectory()) {
            files.push(...walkDir(fullPath, base));
        } else {
            files.push(path.relative(base, fullPath));
        }
    }
    return files;
}

// Copy templates
const templates = {};
const templatesDir = 'templates';
if (fs.existsSync(templatesDir)) {
    fs.mkdirSync('public/templates', { recursive: true });
    for (const name of fs.readdirSync(templatesDir)) {
        const src = path.join(templatesDir, name);
        if (!fs.statSync(src).isDirectory()) continue;
        const dest = path.join('public/templates', name);
        copyDirRecursive(src, dest);
        templates[name] = walkDir(src);
    }
}

// Copy examples
const examples = {};
const examplesDir = 'examples';
if (fs.existsSync(examplesDir)) {
    fs.mkdirSync('public/examples', { recursive: true });
    for (const name of fs.readdirSync(examplesDir)) {
        const src = path.join(examplesDir, name);
        if (!fs.statSync(src).isDirectory()) continue;
        const dest = path.join('public/examples', name);
        copyDirRecursive(src, dest);
        examples[name] = walkDir(src);
    }
}

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
