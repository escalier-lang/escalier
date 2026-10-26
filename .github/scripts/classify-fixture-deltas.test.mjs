import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import {
    classifyFixture,
    diffDiagnostics,
    parseDiagnostics,
} from './classify-fixture-deltas.mjs';

const missingZ = `lib/index.esc:9:12: object is missing property: z

9:  val {x, y, z} = p
               ^
`;

const immutable = `lib/index.esc:4:5: cannot constrain immutable object <: mutable object

4:      obj3.bar = "hello"
        ^^^^^^^^^^^^^^^^^^
`;

const source = 'val p = {x: 1, y: 2}\n';

describe('parseDiagnostics', () => {
    test('reads each header and skips the snippet lines', () => {
        assert.deepEqual(parseDiagnostics(missingZ + immutable), [
            {
                file: 'lib/index.esc',
                line: 9,
                column: 12,
                message: 'object is missing property: z',
            },
            {
                file: 'lib/index.esc',
                line: 4,
                column: 5,
                message: 'cannot constrain immutable object <: mutable object',
            },
        ]);
    });
});

describe('diffDiagnostics', () => {
    test('a diagnostic that moves lines is neither removed nor added', () => {
        const before = parseDiagnostics(missingZ);
        const after = parseDiagnostics(missingZ.replace(':9:', ':11:'));
        assert.deepEqual(diffDiagnostics(before, after), {
            removed: [],
            added: [],
            reworded: [],
        });
    });

    test('a new message at the same position is a rewording', () => {
        const before = parseDiagnostics(missingZ);
        const after = parseDiagnostics(
            missingZ.replace('object is missing', 'missing'),
        );
        const { removed, added, reworded } = diffDiagnostics(before, after);
        assert.deepEqual(removed, []);
        assert.deepEqual(added, []);
        assert.equal(reworded.length, 1);
    });
});

// Each case lists the changed files and the tier and kind of every finding.
const cases = [
    {
        name: 'removed diagnostic on unchanged source',
        files: {
            'error.txt': { before: missingZ + immutable, after: immutable },
        },
        expected: [['high', 'diagnostic-removed']],
    },
    {
        name: 'removed diagnostic after a source edit',
        files: {
            'error.txt': { before: missingZ, after: null },
            'lib/index.esc': { before: source, after: `${source}// edit\n` },
        },
        expected: [['medium', 'diagnostic-removed']],
    },
    {
        name: 'new diagnostic on unchanged source',
        files: { 'error.txt': { before: null, after: immutable } },
        expected: [['medium', 'diagnostic-added']],
    },
    {
        name: 'new diagnostic after a source edit',
        files: {
            'error.txt': { before: null, after: immutable },
            'lib/index.esc': { before: source, after: `${source}// edit\n` },
        },
        expected: [['low', 'diagnostic-added']],
    },
    {
        name: 'reworded diagnostic',
        files: {
            'error.txt': {
                before: missingZ,
                after: missingZ.replace('object is missing', 'missing'),
            },
        },
        expected: [['low', 'diagnostic-reworded']],
    },
    {
        name: 'inferred type change on unchanged source',
        files: {
            'build/lib/index.d.ts': {
                before: 'declare const x: number;\n',
                after: 'declare const x: 1;\n',
            },
        },
        expected: [['medium', 'types-changed']],
    },
    {
        name: 'emitted JS and source map change',
        files: {
            'build/lib/index.js': { before: 'a', after: 'b' },
            'build/lib/index.js.map': { before: 'a', after: 'b' },
        },
        expected: [
            ['medium', 'js-changed'],
            ['low', 'sourcemap-changed'],
        ],
    },
    {
        name: 'new fixture',
        files: {
            'lib/index.esc': { before: null, after: source },
            'package.json': { before: null, after: '{}' },
        },
        expected: [['low', 'fixture-added']],
    },
    {
        name: 'removed fixture',
        files: {
            'lib/index.esc': { before: source, after: null },
            'package.json': { before: '{}', after: null },
        },
        expected: [['medium', 'fixture-removed']],
    },
    {
        name: 'disabled fixture',
        files: { DISABLED: { before: null, after: 'until M3\n' } },
        expected: [['medium', 'fixture-disabled']],
    },
];

describe('classifyFixture', () => {
    for (const { name, files, expected } of cases) {
        test(name, () => {
            const actual = classifyFixture(files).map((f) => [f.tier, f.kind]);
            assert.deepEqual(actual, expected);
        });
    }

    test('types-changed lists the declarations that changed', () => {
        const [finding] = classifyFixture({
            'build/lib/index.d.ts': {
                before: 'declare const x: number;\ndeclare const y: string;\n',
                after: 'declare const x: 1;\ndeclare const y: string;\n',
            },
        });
        assert.deepEqual(finding.details, [
            '- declare const x: number;',
            '+ declare const x: 1;',
        ]);
    });
});
