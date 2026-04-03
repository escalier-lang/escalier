import { Volume, createFsFromVolume } from 'memfs';
import { beforeEach, describe, expect, test } from 'vitest';

import { main } from './copy-files.js';

function createFs(files) {
    const vol = Volume.fromJSON(files);
    return createFsFromVolume(vol);
}

describe('copy-files main', () => {
    let fs;

    beforeEach(() => {
        fs = createFs({
            // Minimal TypeScript lib files
            'node_modules/typescript/lib/lib.es5.d.ts': '// es5 types',
            'node_modules/typescript/lib/lib.dom.d.ts': '// dom types',
            'node_modules/typescript/lib/lib.es2015.core.d.ts':
                '// es2015 core',
            'node_modules/typescript/lib/lib.es2015.promise.d.ts':
                '// es2015 promise',
            // Template
            'templates/single-package/escalier.toml':
                '[project]\nname = "my-project"',
            'templates/single-package/package.json': '{"name":"my-project"}',
            'templates/single-package/lib/index.esc':
                'export fn greet() -> string { return "hi" }',
            'templates/single-package/bin/main.esc': 'console.log(greet())',
            // Example
            'examples/hello-world/escalier.toml':
                '[project]\nname = "hello-world"',
            'examples/hello-world/package.json': '{"name":"hello-world"}',
            'examples/hello-world/lib/index.esc':
                'export fn greet(name: string) -> string { return name }',
            'examples/hello-world/bin/main.esc':
                'val msg = greet("World")\nconsole.log(msg)',
        });
    });

    test('copies TypeScript lib files to public/types', () => {
        main(fs);

        expect(fs.readFileSync('public/types/lib.es5.d.ts', 'utf8')).toBe(
            '// es5 types',
        );
        expect(fs.readFileSync('public/types/lib.dom.d.ts', 'utf8')).toBe(
            '// dom types',
        );
        expect(
            fs.readFileSync('public/types/lib.es2015.core.d.ts', 'utf8'),
        ).toBe('// es2015 core');
        expect(
            fs.readFileSync('public/types/lib.es2015.promise.d.ts', 'utf8'),
        ).toBe('// es2015 promise');
    });

    test('copies templates to public/templates', () => {
        main(fs);

        expect(
            fs.readFileSync(
                'public/templates/single-package/escalier.toml',
                'utf8',
            ),
        ).toBe('[project]\nname = "my-project"');
        expect(
            fs.readFileSync(
                'public/templates/single-package/lib/index.esc',
                'utf8',
            ),
        ).toContain('export fn greet');
        expect(
            fs.readFileSync(
                'public/templates/single-package/bin/main.esc',
                'utf8',
            ),
        ).toBe('console.log(greet())');
    });

    test('copies examples to public/examples', () => {
        main(fs);

        expect(
            fs.readFileSync(
                'public/examples/hello-world/escalier.toml',
                'utf8',
            ),
        ).toBe('[project]\nname = "hello-world"');
        expect(
            fs.readFileSync(
                'public/examples/hello-world/lib/index.esc',
                'utf8',
            ),
        ).toContain('export fn greet');
    });

    test('writes manifest.json with types, templates, and examples', () => {
        const manifest = main(fs);

        expect(manifest.types).toEqual([
            'lib.es5.d.ts',
            'lib.dom.d.ts',
            'lib.es2015.core.d.ts',
            'lib.es2015.promise.d.ts',
        ]);
        expect(manifest.templates['single-package'].sort()).toEqual([
            'bin/main.esc',
            'escalier.toml',
            'lib/index.esc',
            'package.json',
        ]);
        expect(manifest.examples['hello-world'].sort()).toEqual([
            'bin/main.esc',
            'escalier.toml',
            'lib/index.esc',
            'package.json',
        ]);

        // Verify it was also written to disk
        const written = JSON.parse(
            fs.readFileSync('public/types/manifest.json', 'utf8'),
        );
        expect(written).toEqual(manifest);
    });

    test('handles missing templates directory', () => {
        fs = createFs({
            'node_modules/typescript/lib/lib.es5.d.ts': '// es5',
            'node_modules/typescript/lib/lib.dom.d.ts': '// dom',
            'examples/hello-world/escalier.toml': '[project]\nname = "hw"',
            'examples/hello-world/package.json': '{"name":"hw"}',
        });

        const manifest = main(fs);

        expect(manifest.templates).toEqual({});
        expect(manifest.examples['hello-world'].sort()).toEqual([
            'escalier.toml',
            'package.json',
        ]);
    });

    test('handles missing examples directory', () => {
        fs = createFs({
            'node_modules/typescript/lib/lib.es5.d.ts': '// es5',
            'node_modules/typescript/lib/lib.dom.d.ts': '// dom',
            'templates/single-package/escalier.toml':
                '[project]\nname = "proj"',
            'templates/single-package/package.json': '{"name":"proj"}',
        });

        const manifest = main(fs);

        expect(manifest.examples).toEqual({});
        expect(manifest.templates['single-package'].sort()).toEqual([
            'escalier.toml',
            'package.json',
        ]);
    });

    test('handles no es2015 lib files', () => {
        fs = createFs({
            'node_modules/typescript/lib/lib.es5.d.ts': '// es5',
            'node_modules/typescript/lib/lib.dom.d.ts': '// dom',
        });

        const manifest = main(fs);

        expect(manifest.types).toEqual(['lib.es5.d.ts', 'lib.dom.d.ts']);
    });

    test('skips non-directory entries in templates/examples dirs', () => {
        fs = createFs({
            'node_modules/typescript/lib/lib.es5.d.ts': '// es5',
            'node_modules/typescript/lib/lib.dom.d.ts': '// dom',
            'templates/README.md': '# readme',
            'templates/single-package/escalier.toml': '[project]\nname = "p"',
            'templates/single-package/package.json': '{"name":"p"}',
        });

        const manifest = main(fs);

        expect(Object.keys(manifest.templates)).toEqual(['single-package']);
        // README.md should not have been copied
        expect(fs.existsSync('public/templates/README.md')).toBe(false);
    });
});
