import { expect, test } from 'vitest';

import {
    MAX_ISSUE_URL_CHARS,
    NEW_ISSUE_URL,
    crashIssueUrl,
    crashNotification,
    firstOfEachPanic,
    panicNotification,
} from './crash-notification';

const userAgent = 'TestBrowser/1.0';
const report =
    'panic: TODO - TraverseExpr\n\ngoroutine 9 [running]:\nmain.main()\n';

test.each([
    [
        'restarted' as const,
        'The language server crashed and was restarted.',
        'warning',
    ],
    [
        'waiting-for-edit' as const,
        'The language server crashed again on the same code. It will restart when you next edit a file.',
        'error',
    ],
])('crashNotification for %s', (kind, message, type) => {
    expect(crashNotification({ kind, report }, userAgent)).toEqual({
        message,
        type,
        crashReport: {
            headline: 'panic: TODO - TraverseExpr',
            text: `${message}\nBrowser: TestBrowser/1.0\n\npanic: TODO - TraverseExpr\n\ngoroutine 9 [running]:\nmain.main()\n`,
        },
    });
});

test('panicNotification puts the message above the stack', () => {
    const message =
        'the Escalier language server crashed while handling textDocument/hover: boom';
    expect(
        panicNotification(
            { message, stack: 'goroutine 7 [running]:\nmain.main()\n' },
            userAgent,
        ),
    ).toEqual({
        message: 'The language server hit an internal error and recovered.',
        type: 'warning',
        crashReport: {
            headline: message,
            text: `The language server hit an internal error and recovered.\nBrowser: TestBrowser/1.0\n\n${message}\n\ngoroutine 7 [running]:\nmain.main()\n`,
        },
    });
});

/** Returns the title and body that url fills in on a new issue. */
function issueFields(url: string): { title: string; body: string } {
    expect(url.startsWith(`${NEW_ISSUE_URL}?`)).toBe(true);
    const params = new URL(url).searchParams;
    return { title: params.get('title') ?? '', body: params.get('body') ?? '' };
}

test('crashIssueUrl fills in the headline and the whole report', () => {
    const { title, body } = issueFields(
        crashIssueUrl({ headline: 'panic: boom', text: 'line 1\nline 2\n' }),
    );
    expect(title).toBe('Playground language server crash: panic: boom');
    expect(body).toBe(
        [
            '## What happened',
            '',
            '<!-- What were you doing when the language server crashed? Paste the code that triggers it if you can. -->',
            '',
            '## Crash report',
            '',
            '```',
            'line 1\nline 2',
            '```',
            '',
        ].join('\n'),
    );
});

test.each([
    ['plain text', 'x'.repeat(20_000)],
    // Each `/` encodes to three characters, so a stack of paths fills the link faster than
    // its length suggests.
    ['paths', '/home/runner/work/escalier/source_map.go:374\n'.repeat(500)],
])(
    'crashIssueUrl cuts a long report short to fit the link: %s',
    (_name, text) => {
        const url = crashIssueUrl({ headline: 'h'.repeat(200), text });
        expect(url.length).toBeLessThanOrEqual(MAX_ISSUE_URL_CHARS);

        const { title, body } = issueFields(url);
        expect(title).toBe(
            `Playground language server crash: ${'h'.repeat(119)}…`,
        );
        expect(body).toContain(
            'The report below is cut short. Paste the full report, which **Copy report** puts on the clipboard.',
        );
        const shown = body.split('```\n')[1].replace(/\n…\n$/, '');
        expect(text.startsWith(shown)).toBe(true);
        // The link holds most of its budget, not a sliver of the report.
        expect(url.length).toBeGreaterThan(MAX_ISSUE_URL_CHARS - 100);
    },
);

test('firstOfEachPanic shows each message once', () => {
    const shown: string[] = [];
    const show = firstOfEachPanic((report) => shown.push(report.message));

    show({ message: 'a', stack: '1' });
    show({ message: 'b', stack: '2' });
    show({ message: 'a', stack: '3' });

    expect(shown).toEqual(['a', 'b']);
});
