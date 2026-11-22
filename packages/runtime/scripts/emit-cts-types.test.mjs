import { describe, expect, test } from 'vitest';

import { toCtsDeclaration } from './emit-cts-types.mjs';

describe('toCtsDeclaration', () => {
    test.each([
        [`import './polyfills.js';`, `import './polyfills.cjs';`],
        [`export * from './extractor.js';`, `export * from './extractor.cjs';`],
        [
            `export type { ValueType } from "../value_type.js";`,
            `export type { ValueType } from "../value_type.cjs";`,
        ],
        // A bare specifier names another package, so it stays as written.
        [`import 'some-package/index.js';`, `import 'some-package/index.js';`],
    ])('%s', (source, expected) => {
        expect(toCtsDeclaration(source)).toBe(expected);
    });
});
