import { expect, test } from 'vitest';

import {
    type BugReport,
    LINK_LEFT_OUT_NOTE,
    bugReportText,
    bugReportUrl,
    environmentText,
    errorsText,
} from './bug-report';
import { MAX_ISSUE_URL_CHARS, PLAYGROUND_BUG_FORM } from './issue-link';

const report: BugReport = {
    whatHappened: '  Hover shows nothing.\nIt used to show the type.  ',
    expected: 'The type of the value.',
    playgroundLink: 'https://example.com/#project=abc',
    errors: [
        { path: '/lib/index.esc', line: 3, column: 5, message: 'b' },
        { path: '/bin/main.esc', line: 1, column: 1, message: 'a' },
        { path: '/lib/index.esc', line: 1, column: 9, message: 'c' },
    ],
    environment: environmentText('TestBrowser/1.0', 'abc123'),
};

/** Returns the query parameters url sets on the new-issue page. */
function params(url: string): Record<string, string> {
    return Object.fromEntries(new URL(url).searchParams);
}

test.each([
    { commit: 'abc123', expected: 'Playground build: abc123' },
    { commit: undefined, expected: 'Playground build: unknown' },
])('environmentText names the build $commit', ({ commit, expected }) => {
    expect(environmentText('TestBrowser/1.0', commit)).toBe(
        `Browser: TestBrowser/1.0\n${expected}`,
    );
});

test('errorsText sorts the errors by place', () => {
    expect(errorsText(report.errors)).toBe(
        [
            '/bin/main.esc:1:1: a',
            '/lib/index.esc:1:9: c',
            '/lib/index.esc:3:5: b',
        ].join('\n'),
    );
    expect(errorsText([])).toBe('');
});

test('bugReportUrl fills in each field of the form', () => {
    expect(params(bugReportUrl(report))).toEqual({
        template: PLAYGROUND_BUG_FORM,
        title: 'Playground: Hover shows nothing.',
        'what-happened': 'Hover shows nothing.\nIt used to show the type.',
        expected: 'The type of the value.',
        'playground-link': 'https://example.com/#project=abc',
        errors: errorsText(report.errors),
        environment: 'Browser: TestBrowser/1.0\nPlayground build: abc123',
    });
});

test('bugReportUrl titles a report without a description', () => {
    expect(params(bugReportUrl({ ...report, whatHappened: ' ' })).title).toBe(
        'Playground: Bug report',
    );
});

test('bugReportUrl leaves out a link that crowds out the crash report', () => {
    const url = bugReportUrl({
        ...report,
        playgroundLink: `https://example.com/#project=${'a'.repeat(6500)}`,
        crashReport: 'x'.repeat(20_000),
    });

    expect(url.length).toBeLessThanOrEqual(MAX_ISSUE_URL_CHARS);
    const fields = params(url);
    expect(fields['playground-link']).toBe(LINK_LEFT_OUT_NOTE);
    expect(fields['crash-report'].length).toBeGreaterThan(5000);
});

test('bugReportText holds a section for each field with text', () => {
    expect(bugReportText({ ...report, expected: '', crashReport: 'boom' }))
        .toBe(`### What happened

Hover shows nothing.
It used to show the type.

### Playground link

https://example.com/#project=abc

### Errors shown

\`\`\`text
/bin/main.esc:1:1: a
/lib/index.esc:1:9: c
/lib/index.esc:3:5: b
\`\`\`

### Crash report

\`\`\`text
boom
\`\`\`

### Environment

\`\`\`text
Browser: TestBrowser/1.0
Playground build: abc123
\`\`\`
`);
});
