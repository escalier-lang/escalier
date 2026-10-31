// Reports how many declarations a set of names transitively reaches.
//
// Part of the analysis behind ../README.md. It answers what moving a
// declaration between packages would drag along, which is the measurement
// behind the listener placement that README records. Run it from this
// directory with one or more declaration names:
//
//   node closure.mjs WindowEventMap
//   TRACE=WebGLRenderingContext node closure.mjs GlobalEventHandlersEventMap
//
// TREE points at the converted tree and defaults to
// internal/interop/data. TRACE names one declaration in the closure and
// prints the path the walk took to reach it.
//
// The reference extraction is textual, not the converter's own TypeRefNames.
// It strips comments and string literals from each declaration body and takes
// every remaining capitalized identifier, so a reference it reports is a name
// that body mentions rather than one the type checker resolved. The printed
// declaration total is the check on that: it matches
// `grep -c '^export declare '` over the same files, so a parse that silently
// skipped a declaration would show up as a smaller total.

import { readFileSync, readdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));
const TREE =
    process.env.TREE ?? join(HERE, '../../../../internal/interop/data');

// A declaration starts at column 0 with `export declare <kind> <Name>` and owns
// every line up to the next one that does. An `@js(...)` decorator sits on its
// own line above, so it lands in the previous declaration's body, where
// stripping strings leaves nothing that reads as a reference.
const OPEN =
    /^(?:@[\w.]+(?:\([^)]*\))?\s*)?export declare (interface|class|type|fn|val|var|enum|namespace)\s+([A-Za-z_$][\w$]*)/;

function parse(path, uri) {
    const decls = [];
    let cur = null;
    for (const line of readFileSync(path, 'utf8').split('\n')) {
        const m = OPEN.exec(line);
        if (m) {
            cur = { uri, name: m[2], body: [line] };
            decls.push(cur);
        } else if (cur) {
            cur.body.push(line);
        }
    }
    return decls;
}

const all = [];
for (const scheme of ['web', 'std']) {
    const dir = join(TREE, scheme);
    for (const f of readdirSync(dir).filter((x) => x.endsWith('.esc'))) {
        all.push(
            ...parse(join(dir, f), `${scheme}:${f.replace(/\.esc$/, '')}`),
        );
    }
}

// The first declaration of a name wins. The converter rejects a name two
// packages declare, so a later one would mean the tree is already broken.
const owner = new Map();
const byName = new Map();
for (const d of all) {
    if (owner.has(d.name)) continue;
    owner.set(d.name, d.uri);
    byName.set(d.name, d);
}

const refCache = new Map();
function refsOf(name) {
    if (refCache.has(name)) return refCache.get(name);
    const decl = byName.get(name);
    const out = new Set();
    if (decl) {
        const text = decl.body
            .join('\n')
            .replace(/\/\*[\s\S]*?\*\//g, ' ')
            .replace(/\/\/[^\n]*/g, ' ')
            .replace(/"[^"\n]*"/g, ' ');
        for (const m of text.matchAll(/\b([A-Z][\w$]*)\b/g)) {
            // A single capital is a generic parameter, not a declaration name.
            if (m[1] !== name && m[1].length > 1) out.add(m[1]);
        }
    }
    refCache.set(name, out);
    return out;
}

const seeds = process.argv.slice(2);
if (seeds.length === 0) {
    console.error('usage: node closure.mjs <Name>...');
    process.exit(2);
}
const missing = seeds.filter((s) => !owner.has(s));
if (missing.length > 0) {
    console.error(`not declared in ${TREE}: ${missing.join(', ')}`);
    process.exit(1);
}

// Breadth first, so parent records the shortest path to each name reached.
const seen = new Set(seeds);
const parent = new Map();
const queue = [...seeds];
while (queue.length > 0) {
    const name = queue.shift();
    for (const r of refsOf(name)) {
        if (!owner.has(r) || seen.has(r)) continue;
        seen.add(r);
        parent.set(r, name);
        queue.push(r);
    }
}

const reached = new Map();
for (const name of seen) {
    const uri = owner.get(name);
    if (!reached.has(uri)) reached.set(uri, []);
    reached.get(uri).push(name);
}
const total = new Map();
for (const d of all) total.set(d.uri, (total.get(d.uri) ?? 0) + 1);

console.log(`tree: ${all.length} declarations in ${total.size} packages`);
console.log(`closure of ${seeds.join(', ')}: ${seen.size} declarations\n`);

const rows = [...reached].sort((a, b) => b[1].length - a[1].length);
for (const [uri, names] of rows) {
    console.log(
        `  ${uri.padEnd(20)} ${String(names.length).padStart(5)} / ${total.get(uri)}`,
    );
}

// The first edge into each package, which says why the closure includes it.
console.log('\nreached by:');
for (const [uri, names] of rows) {
    const via = names.find((n) => parent.has(n));
    console.log(
        `  ${uri.padEnd(20)} ${via ? `${parent.get(via)} -> ${via}` : '(seed)'}`,
    );
}

if (process.env.TRACE) {
    if (!seen.has(process.env.TRACE)) {
        console.error(`\n${process.env.TRACE} is not in the closure`);
        process.exit(1);
    }
    const path = [];
    for (let n = process.env.TRACE; n; n = parent.get(n)) path.unshift(n);
    console.log(`\npath to ${process.env.TRACE}:\n  ${path.join('\n  -> ')}`);
}
