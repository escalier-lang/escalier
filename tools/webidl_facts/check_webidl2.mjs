// Compares internal/webidl/table.go with what webidl2 parses from the installed
// `@webref/idl` package, and prints every definition the two disagree on. The
// Go scanner reads only the parts of the grammar the table needs, so this is
// the check that it reads them the way a full WebIDL parser does.
//
// Run it from the repository root after `pnpm install`:
//
//   node tools/webidl_facts/check_webidl2.mjs
//
// It exits non-zero when any definition differs.

import fs from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';

const require = createRequire(path.join(process.cwd(), 'package.json'));
const idl = await import(require.resolve('@webref/idl'));

const kinds = ['interface', 'callback interface', 'namespace'];

// valuesOf returns the identifiers on the right of an extended attribute, as
// the Go table lists them.
function valuesOf(attr) {
    if (!attr) return [];
    if (attr.rhs.type === '*') return ['*'];
    if (Array.isArray(attr.rhs.value))
        return attr.rhs.value.map((v) => v.value);
    return [attr.rhs.value];
}

const want = {};
const partials = [];
// keyOf maps each non-partial definition's identifier to its key in want, which
// differs for an interface with `[LegacyNamespace]`. A partial names the
// identifier it extends.
const keyOf = {};
for (const ast of Object.values(await idl.parseAll())) {
    for (const def of ast) {
        if (!kinds.includes(def.type)) continue;
        if (def.partial) {
            partials.push(def);
            continue;
        }
        const attr = (name) => def.extAttrs.find((a) => a.name === name);
        const ns = attr('LegacyNamespace');
        const key = ns ? `${ns.rhs.value}.${def.name}` : def.name;
        keyOf[def.name] = key;
        want[key] = {
            kind: def.type,
            exposed: valuesOf(attr('Exposed')),
            global: valuesOf(attr('Global')),
            transferable: Boolean(attr('Transferable')),
            constructible: def.members.some((m) => m.type === 'constructor'),
        };
    }
}
let mismatches = 0;
for (const def of partials) {
    const entry = want[keyOf[def.name]];
    if (!entry) {
        mismatches++;
        console.log(`partial ${def.name} extends no definition`);
        continue;
    }
    if (def.extAttrs.some((a) => a.name === 'Transferable'))
        entry.transferable = true;
    if (def.members.some((m) => m.type === 'constructor'))
        entry.constructible = true;
}

const goKinds = {
    KindInterface: 'interface',
    KindCallbackInterface: 'callback interface',
    KindNamespace: 'namespace',
};
const table = fs.readFileSync(
    path.join('internal', 'webidl', 'table.go'),
    'utf8',
);
const got = {};
for (const [, name, body] of table.matchAll(/^\t"([^"]+)":\s+\{(.*)\},$/gm)) {
    const list = (field) => {
        const match = body.match(
            new RegExp(`${field}: \\[\\]string\\{([^}]*)\\}`),
        );
        return match ? match[1].split(', ').map((s) => JSON.parse(s)) : [];
    };
    got[name] = {
        kind: goKinds[body.match(/Kind: (\w+)/)[1]],
        exposed: list('Exposed'),
        global: list('Global'),
        transferable: body.includes('Transferable: true'),
        constructible: body.includes('Constructible: true'),
    };
}

for (const name of new Set([...Object.keys(want), ...Object.keys(got)])) {
    if (JSON.stringify(want[name]) !== JSON.stringify(got[name])) {
        mismatches++;
        console.log(
            `${name}\n  webidl2: ${JSON.stringify(want[name])}\n  table:   ${JSON.stringify(got[name])}`,
        );
    }
}
console.log(
    `${Object.keys(want).length} definitions, ${mismatches} mismatches`,
);
process.exit(mismatches === 0 ? 0 : 1);
