import { describe, expect, test } from 'vitest';

import {
    type DiagnosticSeverity,
    type ProjectDiagnostic,
    type ProjectDiagnostics,
    countOf,
    diagnosticCounts,
    diagnosticLocation,
    forgetRemovedAction,
    projectDiagnosticsReducer,
    projectDiagnosticsText,
} from './project-diagnostics';

function diagnostic(
    path: string,
    line: number,
    message: string,
    severity: DiagnosticSeverity = 'error',
): ProjectDiagnostic {
    return { path, line, column: 1, severity, message };
}

const a1 = diagnostic('/lib/a.esc', 1, 'a1');
const a2 = diagnostic('/lib/a.esc', 2, 'a2', 'warning');
const b1 = diagnostic('/lib/b.esc', 1, 'b1');
const nested = diagnostic('/lib/sub/c.esc', 1, 'c1');

describe('projectDiagnosticsReducer', () => {
    test.each<[string, ProjectDiagnostics, ProjectDiagnostics]>([
        [
            'adds a file',
            { '/lib/a.esc': [a1] },
            { '/lib/a.esc': [a1], '/lib/b.esc': [b1] },
        ],
        ['replaces a file', { '/lib/b.esc': [a1] }, { '/lib/b.esc': [b1] }],
    ])('setFileDiagnostics %s', (_name, before, after) => {
        expect(
            projectDiagnosticsReducer(before, {
                type: 'setFileDiagnostics',
                path: '/lib/b.esc',
                diagnostics: [b1],
            }),
        ).toEqual(after);
    });

    test('setFileDiagnostics with no diagnostics drops the file', () => {
        expect(
            projectDiagnosticsReducer(
                { '/lib/a.esc': [a1], '/lib/b.esc': [b1] },
                {
                    type: 'setFileDiagnostics',
                    path: '/lib/b.esc',
                    diagnostics: [],
                },
            ),
        ).toEqual({ '/lib/a.esc': [a1] });
    });

    test('forgetAll drops every file', () => {
        expect(
            projectDiagnosticsReducer(
                { '/lib/a.esc': [a1], '/lib/b.esc': [b1] },
                { type: 'forgetAll' },
            ),
        ).toEqual({});
    });

    test.each<[string, string, ProjectDiagnostics]>([
        [
            'a file',
            '/lib/a.esc',
            { '/lib/b.esc': [b1], '/lib/sub/c.esc': [nested] },
        ],
        [
            'a directory and the files under it',
            '/lib/sub',
            { '/lib/a.esc': [a1], '/lib/b.esc': [b1] },
        ],
        // `/lib/a` is a prefix of `/lib/a.esc` but not its directory.
        [
            'nothing for a path that only shares a prefix',
            '/lib/a',
            {
                '/lib/a.esc': [a1],
                '/lib/b.esc': [b1],
                '/lib/sub/c.esc': [nested],
            },
        ],
    ])('forgetPath drops %s', (_name, path, after) => {
        expect(
            projectDiagnosticsReducer(
                {
                    '/lib/a.esc': [a1],
                    '/lib/b.esc': [b1],
                    '/lib/sub/c.esc': [nested],
                },
                { type: 'forgetPath', path },
            ),
        ).toEqual(after);
    });
});

test('diagnosticCounts counts the errors and warnings of every file', () => {
    expect(
        diagnosticCounts({
            '/lib/a.esc': [a1, a2, diagnostic('/lib/a.esc', 3, 'a3', 'info')],
            '/lib/b.esc': [b1],
        }),
    ).toEqual({ errors: 2, warnings: 1 });
});

test.each([
    [0, '0 errors'],
    [1, '1 error'],
    [2, '2 errors'],
])('countOf(%i, "error") is "%s"', (count, text) => {
    expect(countOf(count, 'error')).toBe(text);
});

describe('projectDiagnosticsText', () => {
    test('lists every diagnostic with its severity, sorted by path and line', () => {
        expect(
            projectDiagnosticsText({
                '/lib/b.esc': [b1],
                '/lib/a.esc': [a2, a1],
            }),
        ).toBe(
            '/lib/a.esc:1:1: error: a1\n/lib/a.esc:2:1: warning: a2\n/lib/b.esc:1:1: error: b1\n',
        );
    });

    test('says when there are no diagnostics', () => {
        expect(projectDiagnosticsText({})).toBe('No diagnostics.\n');
    });
});

describe('forgetRemovedAction', () => {
    test.each([
        [
            'a deleted file',
            { type: 'delete', kind: 'file', path: '/lib/a.esc' } as const,
            { type: 'forgetPath', path: '/lib/a.esc' },
        ],
        [
            'the old path of a renamed directory',
            {
                type: 'rename',
                kind: 'dir',
                path: '/lib/new',
                oldPath: '/lib/old',
            } as const,
            { type: 'forgetPath', path: '/lib/old' },
        ],
    ])('forgets %s', (_name, event, action) => {
        expect(forgetRemovedAction(event)).toEqual(action);
    });

    test.each([
        { type: 'create', kind: 'file', path: '/lib/a.esc' } as const,
        { type: 'change', kind: 'file', path: '/lib/a.esc' } as const,
    ])('ignores a $type event', (event) => {
        expect(forgetRemovedAction(event)).toBeNull();
    });
});

describe('diagnosticLocation', () => {
    test.each([
        [
            '/lib/index.esc:25:26: error: cannot constrain "a" <: number',
            { path: '/lib/index.esc', line: 25, column: 26 },
        ],
        [
            '/lib/my file.esc:1:2: warning: unused: x',
            { path: '/lib/my file.esc', line: 1, column: 2 },
        ],
        ['No diagnostics.', null],
        ['', null],
    ])('reads %j', (line, location) => {
        expect(diagnosticLocation(line)).toEqual(location);
    });
});
