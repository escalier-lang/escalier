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

/** Returns a Go stack trace whose panic happened in fn, on goroutine number goroutine. */
function goStack(fn: string, goroutine: number): string {
    return [
        `goroutine ${goroutine} [running]:`,
        'main.(*Server).Handle.func1()',
        '\t/src/main.go:176 +0xbf',
        'panic({0xa7cf20, 0xbcf930})',
        '\t/go/src/runtime/panic.go:860 +0x13a',
        `${fn}()`,
        '\t/src/hover.go:10 +0x25',
        '',
    ].join('\n');
}

test('firstOfEachPanic shows a panic once however often it recurs', () => {
    const shown: string[] = [];
    const show = firstOfEachPanic((report) => shown.push(report.message));

    show({ message: 'a', stack: goStack('main.hover', 9) });
    show({ message: 'b', stack: goStack('main.hover', 10) });
    // The same panic on another goroutine.
    show({ message: 'a', stack: goStack('main.hover', 11) });

    expect(shown).toEqual(['a', 'b']);
});

test('firstOfEachPanic shows a message again when it panics somewhere new', () => {
    const sites: string[] = [];
    const show = firstOfEachPanic((report) => sites.push(report.stack));

    show({ message: 'a', stack: goStack('main.hover', 9) });
    show({ message: 'a', stack: goStack('main.complete', 9) });

    expect(sites).toEqual([
        goStack('main.hover', 9),
        goStack('main.complete', 9),
    ]);
});

test('firstOfEachPanic forgets what it has seen after 100 distinct panics', () => {
    const shown: string[] = [];
    const show = firstOfEachPanic((report) => shown.push(report.message));

    for (let i = 0; i <= 100; i++) {
        show({ message: `panic ${i}`, stack: goStack('main.hover', 1) });
    }
    show({ message: 'panic 0', stack: goStack('main.hover', 1) });

    expect(shown).toHaveLength(102);
    expect(shown.at(-1)).toBe('panic 0');
});

test('crashIssueUrl never splits a surrogate pair', () => {
    // Every cut point in a run of emoji falls between or inside a surrogate pair.
    const { title, body } = issueFields(
        crashIssueUrl({
            // The 119-unit cut falls between the two halves of the emoji.
            headline: `${'a'.repeat(118)}😀b`,
            text: '😀'.repeat(10_000),
        }),
    );
    expect(title).toBe(`Playground language server crash: ${'a'.repeat(118)}…`);
    expect(body).not.toContain('\ufffd');
    expect(body).toContain('😀😀\n…\n');
});
