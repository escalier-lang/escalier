import assert from 'node:assert/strict';
import {
    existsSync,
    mkdirSync,
    mkdtempSync,
    readFileSync,
    rmSync,
    writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, beforeEach, describe, test } from 'node:test';

import { copyStdlib } from './copy-stdlib.mjs';

describe('copyStdlib', () => {
    let tmp;
    beforeEach(() => {
        tmp = mkdtempSync(join(tmpdir(), 'copy-stdlib-'));
    });
    afterEach(() => {
        rmSync(tmp, { recursive: true, force: true });
    });

    test('copies the .esc files and removes files the source no longer has', () => {
        const src = join(tmp, 'src');
        const dest = join(tmp, 'dest');
        mkdirSync(join(src, 'std'), { recursive: true });
        writeFileSync(join(src, 'std', 'math.esc'), 'declare val PI: number\n');
        writeFileSync(join(src, 'std', 'README.md'), '# std\n');
        mkdirSync(join(dest, 'web'), { recursive: true });
        writeFileSync(join(dest, 'web', 'stale.esc'), '');

        copyStdlib(src, dest);

        assert.equal(
            readFileSync(join(dest, 'std', 'math.esc'), 'utf8'),
            'declare val PI: number\n',
        );
        assert.equal(existsSync(join(dest, 'std', 'README.md')), false);
        assert.equal(existsSync(join(dest, 'web', 'stale.esc')), false);
    });

    test('rejects a source with no std/ subdirectory', () => {
        const src = join(tmp, 'src');
        const dest = join(tmp, 'dest');
        mkdirSync(src);

        assert.throws(
            () => copyStdlib(src, dest),
            new Error(`${src} has no std/ subdirectory`),
        );
        assert.equal(existsSync(dest), false);
    });
});
