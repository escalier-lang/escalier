import * as monaco from 'monaco-editor-core';
import { expect, test, vi } from 'vitest';

vi.mock('monaco-editor-core');

import { shownErrors } from './editor-errors';

test('shownErrors returns the error markers in open files and skips the rest', () => {
    vi.mocked(monaco.editor.getModelMarkers).mockReturnValue([
        {
            owner: 'escalier',
            resource: { path: '/lib/index.esc' },
            severity: monaco.MarkerSeverity.Error,
            message: 'cannot constrain "a" <: number',
            startLineNumber: 2,
            startColumn: 7,
            endLineNumber: 2,
            endColumn: 10,
        },
        {
            owner: 'escalier',
            resource: { path: '/lib/index.esc' },
            severity: monaco.MarkerSeverity.Warning,
            message: 'unused value',
            startLineNumber: 1,
            startColumn: 1,
            endLineNumber: 1,
            endColumn: 2,
        },
        {
            owner: 'escalier',
            resource: { path: '/lib/closed.esc' },
            severity: monaco.MarkerSeverity.Error,
            message: 'in a closed tab',
            startLineNumber: 1,
            startColumn: 1,
            endLineNumber: 1,
            endColumn: 2,
        },
    ] as unknown as monaco.editor.IMarker[]);

    expect(shownErrors(['/lib/index.esc'])).toEqual([
        {
            path: '/lib/index.esc',
            line: 2,
            column: 7,
            message: 'cannot constrain "a" <: number',
        },
    ]);
});
