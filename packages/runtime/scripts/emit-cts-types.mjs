#!/usr/bin/env node
//
// Writes a `.d.cts` copy of each `.d.ts` file in dist/.
//
//     node scripts/emit-cts-types.mjs
//
// The package is `"type": "module"`, so TypeScript reads every `.d.ts` file
// in it as ESM. The `require` condition in `exports` points at
// dist/index.d.cts instead, which TypeScript reads as CommonJS and matches
// dist/index.cjs. Each copy rewrites its relative `./x.js` specifiers to
// `./x.cjs`, so TypeScript resolves them to the other `.d.cts` files.

import { readdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const DIST = join(dirname(fileURLToPath(import.meta.url)), '..', 'dist');

// Matches the quoted specifier of a relative import or export that ends in
// `.js`, such as `'./extractor.js'` in `export * from './extractor.js'`.
const RELATIVE_JS_SPECIFIER = /(['"])(\.{1,2}\/[^'"]+)\.js\1/g;

// Returns `source`, a `.d.ts` file's text, with each relative `.js`
// specifier rewritten to `.cjs`.
export function toCtsDeclaration(source) {
    return source.replace(RELATIVE_JS_SPECIFIER, '$1$2.cjs$1');
}

function main() {
    for (const file of readdirSync(DIST)) {
        if (!file.endsWith('.d.ts')) {
            continue;
        }
        const source = readFileSync(join(DIST, file), 'utf8');
        const target = file.replace(/\.d\.ts$/, '.d.cts');
        writeFileSync(join(DIST, target), toCtsDeclaration(source));
    }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
    main();
}
