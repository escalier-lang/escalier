import { readFileSync } from 'node:fs';
import { describe, expect, test } from 'vitest';

import {
    type BugFormFields,
    MAX_ISSUE_URL_CHARS,
    NEW_ISSUE_URL,
    PLAYGROUND_BUG_FORM,
    TRUNCATION_NOTE,
    bugFormUrl,
    issueTitle,
    prefixOf,
} from './issue-link';

/** Returns the query parameters url sets on the new-issue page. */
function params(url: string): Record<string, string> {
    expect(url.startsWith(`${NEW_ISSUE_URL}?`)).toBe(true);
    return Object.fromEntries(new URL(url).searchParams);
}

test('the bug form declares exactly the fields BugFormFields names', () => {
    const form = readFileSync(
        new URL(
            `../../../.github/ISSUE_TEMPLATE/${PLAYGROUND_BUG_FORM}`,
            import.meta.url,
        ),
        'utf8',
    );
    const ids = [...form.matchAll(/^\s+id: (\S+)$/gm)].map((m) => m[1]);
    const fields: Required<BugFormFields> = {
        'what-happened': '',
        expected: '',
        'playground-link': '',
        errors: '',
        'crash-report': '',
        environment: '',
    };
    expect(ids).toEqual(Object.keys(fields));
});

test.each([
    { headline: 'short', expected: 'Playground: short' },
    {
        headline: 'h'.repeat(200),
        expected: `Playground: ${'h'.repeat(119)}…`,
    },
    // The 119-unit cut falls between the two halves of the emoji.
    {
        headline: `${'a'.repeat(118)}😀b`,
        expected: `Playground: ${'a'.repeat(118)}…`,
    },
])('issueTitle($headline)', ({ headline, expected }) => {
    expect(issueTitle('Playground', headline)).toBe(expected);
});

test('bugFormUrl fills in the template, the title and each non-empty field', () => {
    expect(
        params(
            bugFormUrl('Playground: hover is blank', {
                'what-happened': 'Hovering shows nothing.',
                expected: '',
                environment: 'Browser: TestBrowser/1.0',
            }),
        ),
    ).toEqual({
        template: PLAYGROUND_BUG_FORM,
        title: 'Playground: hover is blank',
        'what-happened': 'Hovering shows nothing.',
        environment: 'Browser: TestBrowser/1.0',
    });
});

describe('bugFormUrl cuts', () => {
    const longReport = 'x'.repeat(20_000);

    test('a truncate cut keeps the longest prefix that fits', () => {
        const url = bugFormUrl('t', { 'crash-report': longReport }, [
            { field: 'crash-report', kind: 'truncate', keep: 0 },
        ]);

        expect(url.length).toBeLessThanOrEqual(MAX_ISSUE_URL_CHARS);
        expect(url.length).toBeGreaterThan(MAX_ISSUE_URL_CHARS - 10);
        const report = params(url)['crash-report'];
        expect(report.endsWith(`\n${TRUNCATION_NOTE}`)).toBe(true);
        expect(longReport.startsWith(report.split('\n')[0])).toBe(true);
    });

    test('a truncate cut keeps at least keep characters', () => {
        const url = bugFormUrl('t', { 'crash-report': longReport }, [
            { field: 'crash-report', kind: 'truncate', keep: 9000 },
        ]);

        expect(params(url)['crash-report']).toBe(
            `${'x'.repeat(9000)}\n${TRUNCATION_NOTE}`,
        );
    });

    test('an omit cut replaces the field with its note', () => {
        const url = bugFormUrl(
            't',
            { 'playground-link': `https://x/#project=${'a'.repeat(9000)}` },
            [{ field: 'playground-link', kind: 'omit', note: 'Too long.' }],
        );

        expect(params(url)['playground-link']).toBe('Too long.');
    });

    test('cuts stop once the link fits', () => {
        const link = `https://x/#project=${'a'.repeat(3000)}`;
        const url = bugFormUrl(
            't',
            { 'playground-link': link, 'crash-report': longReport },
            [
                { field: 'crash-report', kind: 'truncate', keep: 1500 },
                { field: 'playground-link', kind: 'omit', note: 'Too long.' },
            ],
        );

        expect(params(url)['playground-link']).toBe(link);
        expect(url.length).toBeLessThanOrEqual(MAX_ISSUE_URL_CHARS);
    });

    test('a later cut runs when an earlier one leaves the link too long', () => {
        const url = bugFormUrl(
            't',
            {
                'playground-link': `https://x/#project=${'a'.repeat(6500)}`,
                'crash-report': longReport,
            },
            [
                { field: 'crash-report', kind: 'truncate', keep: 1500 },
                { field: 'playground-link', kind: 'omit', note: 'Too long.' },
                { field: 'crash-report', kind: 'truncate', keep: 0 },
            ],
        );

        const filled = params(url);
        expect(filled['playground-link']).toBe('Too long.');
        expect(filled['crash-report'].length).toBeGreaterThan(5000);
        expect(url.length).toBeLessThanOrEqual(MAX_ISSUE_URL_CHARS);
    });

    test('a field no longer than its keep stays whole, with no note', () => {
        const short = 'y'.repeat(800);
        const url = bugFormUrl(
            't',
            {
                'playground-link': `https://x/#project=${'a'.repeat(9000)}`,
                'crash-report': short,
            },
            [
                { field: 'crash-report', kind: 'truncate', keep: 1500 },
                { field: 'playground-link', kind: 'omit', note: 'Too long.' },
            ],
        );

        expect(params(url)['crash-report']).toBe(short);
    });

    test('a field a later cut gives room for takes back all its text, with no note', () => {
        const report = 'x'.repeat(3000);
        const url = bugFormUrl(
            't',
            {
                'playground-link': `https://x/#project=${'a'.repeat(9000)}`,
                'crash-report': report,
            },
            [
                { field: 'crash-report', kind: 'truncate', keep: 0 },
                { field: 'playground-link', kind: 'omit', note: 'Too long.' },
            ],
        );

        expect(params(url)['crash-report']).toBe(report);
    });

    test('a field an omit cut left out stays left out', () => {
        const url = bugFormUrl('t', { 'crash-report': longReport }, [
            { field: 'crash-report', kind: 'truncate', keep: 9000 },
            { field: 'crash-report', kind: 'omit', note: 'Too long.' },
        ]);

        expect(params(url)['crash-report']).toBe('Too long.');
    });

    test('a cut never splits a surrogate pair', () => {
        const url = bugFormUrl('t', { 'crash-report': '😀'.repeat(10_000) }, [
            { field: 'crash-report', kind: 'truncate', keep: 0 },
        ]);

        expect(params(url)['crash-report']).not.toContain('�');
    });
});

test.each([
    { text: 'abc', length: 2, expected: 'ab' },
    { text: 'a😀', length: 2, expected: 'a' },
    { text: 'a😀', length: 3, expected: 'a😀' },
])('prefixOf($text, $length) is $expected', ({ text, length, expected }) => {
    expect(prefixOf(text, length)).toBe(expected);
});
