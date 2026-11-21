import { describe, expect, test, vi } from 'vitest';

vi.mock('monaco-editor-core');
// Mock the language module to avoid Monaco language registration side effects.
vi.mock('#language/language', () => ({ languageID: 'escalier' }));

import { DIAGNOSTICS_TAB_PATH } from '#diagnostics/project-diagnostics';

import { displayName, languageForPath } from './paths';

describe('languageForPath', () => {
    test.each([
        { path: '/lib/index.esc', language: 'escalier' },
        { path: '/build/lib/index.js', language: 'javascript' },
        { path: '/build/lib/index.d.ts', language: 'typescript' },
        { path: '/package.json', language: 'json' },
        { path: '/build/lib/index.js.map', language: 'json' },
        { path: '/escalier.toml', language: 'toml' },
        { path: '/README.md', language: 'plaintext' },
        { path: DIAGNOSTICS_TAB_PATH, language: 'escalier-diagnostics' },
    ])('maps $path to $language', ({ path, language }) => {
        expect(languageForPath(path)).toBe(language);
    });
});

test.each([
    { path: '/lib/index.esc', name: 'index.esc' },
    { path: '/escalier.toml', name: 'escalier.toml' },
])('displayName($path) is $name', ({ path, name }) => {
    expect(displayName(path)).toBe(name);
});
