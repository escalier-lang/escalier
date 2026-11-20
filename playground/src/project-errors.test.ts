import { describe, expect, test } from 'vitest';

import type { ShownError } from './bug-report';
import {
    type ProjectErrors,
    errorCount,
    errorCountLabel,
    forgetRemovedAction,
    projectErrorsReducer,
    projectErrorsText,
} from './project-errors';

function shownError(path: string, line: number, message: string): ShownError {
    return { path, line, column: 1, message };
}

const a1 = shownError('/lib/a.esc', 1, 'a1');
const a2 = shownError('/lib/a.esc', 2, 'a2');
const b1 = shownError('/lib/b.esc', 1, 'b1');
const nested = shownError('/lib/sub/c.esc', 1, 'c1');

describe('projectErrorsReducer', () => {
    test.each<[string, ProjectErrors, ProjectErrors]>([
        [
            'adds a file',
            { '/lib/a.esc': [a1] },
            { '/lib/a.esc': [a1], '/lib/b.esc': [b1] },
        ],
        ['replaces a file', { '/lib/b.esc': [a1] }, { '/lib/b.esc': [b1] }],
    ])('setFileErrors %s', (_name, before, after) => {
        expect(
            projectErrorsReducer(before, {
                type: 'setFileErrors',
                path: '/lib/b.esc',
                errors: [b1],
            }),
        ).toEqual(after);
    });

    test('forgetAll drops every file', () => {
        expect(
            projectErrorsReducer(
                { '/lib/a.esc': [a1], '/lib/b.esc': [b1] },
                { type: 'forgetAll' },
            ),
        ).toEqual({});
    });

    test('setFileErrors with no errors drops the file', () => {
        expect(
            projectErrorsReducer(
                { '/lib/a.esc': [a1], '/lib/b.esc': [b1] },
                { type: 'setFileErrors', path: '/lib/b.esc', errors: [] },
            ),
        ).toEqual({ '/lib/a.esc': [a1] });
    });

    test.each<[string, string, ProjectErrors]>([
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
            projectErrorsReducer(
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

test('errorCount adds up the errors of every file', () => {
    expect(errorCount({ '/lib/a.esc': [a1, a2], '/lib/b.esc': [b1] })).toBe(3);
});

test.each([
    [0, 'No errors'],
    [1, '1 error'],
    [2, '2 errors'],
])('errorCountLabel(%i) is "%s"', (count, label) => {
    expect(errorCountLabel(count)).toBe(label);
});

describe('projectErrorsText', () => {
    test('lists every error sorted by path and line', () => {
        expect(
            projectErrorsText({ '/lib/b.esc': [b1], '/lib/a.esc': [a2, a1] }),
        ).toBe('/lib/a.esc:1:1: a1\n/lib/a.esc:2:1: a2\n/lib/b.esc:1:1: b1\n');
    });

    test('says when there are no errors', () => {
        expect(projectErrorsText({})).toBe('No errors.\n');
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
