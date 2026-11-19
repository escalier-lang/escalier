import { expect, test, vi } from 'vitest';

import { LINK_LEFT_OUT_NOTE } from './bug-report';
import {
    crashIssueUrl,
    crashNotification,
    crashReportText,
    crashToasts,
    firstOfEachPanic,
    panicNotification,
    showServerCrashes,
} from './crash-notification';
import {
    MAX_ISSUE_URL_CHARS,
    NEW_ISSUE_URL,
    TRUNCATION_NOTE,
} from './issue-link';
import type { PanicReport, ServerCrashEvent } from './lsp-client/client';
import { MAX_PERMALINK_CHARS } from './permalink';

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

const environment = 'Browser: TestBrowser/1.0\nPlayground build: abc123';

/** Returns the fields that url fills in on the playground bug form. */
function formFields(url: string): Record<string, string> {
    expect(url.startsWith(`${NEW_ISSUE_URL}?`)).toBe(true);
    return Object.fromEntries(new URL(url).searchParams);
}

test('crashIssueUrl fills in the bug form with the report and the environment', () => {
    expect(
        formFields(
            crashIssueUrl(
                { headline: 'panic: boom', text: 'line 1\nline 2\n' },
                environment,
            ),
        ),
    ).toEqual({
        template: 'playground-bug.yml',
        title: 'Playground language server crash: panic: boom',
        'crash-report': 'line 1\nline 2',
        environment,
    });
});

test('crashIssueUrl fills in the playground link', () => {
    const permalink = 'https://example.com/playground/#project=abc_-';

    expect(
        formFields(
            crashIssueUrl(
                { headline: 'panic: boom', text: 'line 1\n', permalink },
                environment,
            ),
        )['playground-link'],
    ).toBe(permalink);
});

test.each([
    ['plain text', 'x'.repeat(20_000)],
    // Each `/` encodes to three characters, so a stack of paths fills the link faster than
    // its length suggests.
    ['paths', '/home/runner/work/escalier/source_map.go:374\n'.repeat(500)],
])(
    'crashIssueUrl cuts a long report short to fit the link: %s',
    (_name, text) => {
        const url = crashIssueUrl(
            { headline: 'h'.repeat(200), text },
            environment,
        );
        expect(url.length).toBeLessThanOrEqual(MAX_ISSUE_URL_CHARS);

        const fields = formFields(url);
        expect(fields.title).toBe(
            `Playground language server crash: ${'h'.repeat(119)}…`,
        );
        const crashReport = fields['crash-report'] ?? '';
        expect(crashReport.endsWith(`\n${TRUNCATION_NOTE}`)).toBe(true);
        const shown = crashReport.slice(0, -`\n${TRUNCATION_NOTE}`.length);
        expect(text.startsWith(shown)).toBe(true);
        // The link holds most of its budget, not a sliver of the report.
        expect(url.length).toBeGreaterThan(MAX_ISSUE_URL_CHARS - 100);
    },
);

test('crashIssueUrl never splits a surrogate pair', () => {
    // Every cut point in a run of emoji falls between or inside a surrogate pair.
    const fields = formFields(
        crashIssueUrl(
            {
                // The 119-unit cut falls between the two halves of the emoji.
                headline: `${'a'.repeat(118)}😀b`,
                text: '😀'.repeat(10_000),
            },
            environment,
        ),
    );
    expect(fields.title).toBe(
        `Playground language server crash: ${'a'.repeat(118)}…`,
    );
    expect(fields['crash-report']).not.toContain('\ufffd');
    expect(fields['crash-report']).toContain(`😀😀\n${TRUNCATION_NOTE}`);
});

test('crashIssueUrl leaves out a playground link that crowds out the report', () => {
    const longLink = `https://example.com/#project=${'a'.repeat(6500)}`;
    const url = crashIssueUrl(
        {
            headline: 'panic: boom',
            text: 'x'.repeat(20_000),
            permalink: longLink,
        },
        environment,
    );

    expect(url.length).toBeLessThanOrEqual(MAX_ISSUE_URL_CHARS);
    const fields = formFields(url);
    expect(fields['playground-link']).toBe(LINK_LEFT_OUT_NOTE);
    expect(fields['crash-report']).toContain('x'.repeat(5000));
});

test('crashIssueUrl keeps a playground link that leaves room for the report', () => {
    const link = `https://example.com/#project=${'a'.repeat(3000)}`;
    const url = crashIssueUrl(
        { headline: 'panic: boom', text: 'x'.repeat(20_000), permalink: link },
        environment,
    );

    expect(url.length).toBeLessThanOrEqual(MAX_ISSUE_URL_CHARS);
    const fields = formFields(url);
    expect(fields['playground-link']).toBe(link);
    expect(fields['crash-report']).toContain('x'.repeat(1500));
});
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

const permalink = 'https://example.com/playground/#project=abc_-';

test('a crash report carries the permalink, which its text ends with', () => {
    const { crashReport } = crashNotification(
        { kind: 'restarted', report },
        userAgent,
        permalink,
    );

    expect(crashReport?.permalink).toBe(permalink);
    expect(crashReport && crashReportText(crashReport)).toBe(
        `The language server crashed and was restarted.\nBrowser: TestBrowser/1.0\n\npanic: TODO - TraverseExpr\n\ngoroutine 9 [running]:\nmain.main()\n\nPlayground link: ${permalink}\n`,
    );
});

test('a crash report says when the project is too large to link to', () => {
    const tooLong = `https://example.com/#project=${'a'.repeat(MAX_PERMALINK_CHARS)}`;
    const { crashReport } = panicNotification(
        { message: 'boom', stack: 'goroutine 7 [running]:\n' },
        userAgent,
        tooLong,
    );

    expect(crashReport).toEqual({
        headline: 'boom',
        text: 'The language server hit an internal error and recovered.\nBrowser: TestBrowser/1.0\nPlayground link: none, because the project is too large to link to.\n\nboom\n\ngoroutine 7 [running]:\n',
    });
});

/** Returns a promise along with the function that resolves it. */
function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
    let resolve: (value: T) => void = () => {};
    const promise = new Promise<T>((r) => {
        resolve = r;
    });
    return { promise, resolve };
}

/** Returns a notification whose message records the permalink it was built with. */
function linkToast(name: string) {
    return (permalink: string | undefined) => ({
        message: `${name}: ${permalink ?? 'no link'}`,
        type: 'warning' as const,
    });
}

test('crashToasts shows toasts in call order when a later link is ready first', async () => {
    const links = [deferred<string>(), deferred<string>()];
    let call = 0;
    const shown: string[] = [];
    const show = crashToasts(
        () => links[call++].promise,
        (notification) => shown.push(notification.message),
    );

    show(linkToast('first'));
    show(linkToast('second'));
    links[1].resolve('link 2');
    await vi.waitFor(() => expect(call).toBe(2));
    expect(shown).toEqual([]);
    links[0].resolve('link 1');

    await vi.waitFor(() =>
        expect(shown).toEqual(['first: link 1', 'second: link 2']),
    );
});

test('crashToasts still shows later toasts after one throws', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    const shown: string[] = [];
    const show = crashToasts(
        () => Promise.resolve('link'),
        (notification) => shown.push(notification.message),
    );

    show(() => {
        throw new Error('bad toast');
    });
    show(linkToast('next'));

    await vi.waitFor(() => expect(shown).toEqual(['next: link']));
    expect(console.error).toHaveBeenCalledWith(
        'Failed to show a crash toast:',
        new Error('bad toast'),
    );
});

test('crashToasts shows the toast without a link when capture fails', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    const shown: string[] = [];
    const show = crashToasts(
        () => Promise.reject(new Error('no CompressionStream')),
        (notification) => shown.push(notification.message),
    );

    show(linkToast('crash'));

    await vi.waitFor(() => expect(shown).toEqual(['crash: no link']));
});

test('showServerCrashes shows each crash and the first of each panic with a link', async () => {
    let onCrash: (event: ServerCrashEvent) => void = () => {};
    let onPanic: (report: PanicReport) => void = () => {};
    const client = {
        onServerCrash: (callback: (event: ServerCrashEvent) => void) => {
            onCrash = callback;
        },
        onServerPanic: (callback: (report: PanicReport) => void) => {
            onPanic = callback;
        },
    };
    const shown: (string | undefined)[] = [];
    showServerCrashes(
        client,
        userAgent,
        () => Promise.resolve(permalink),
        (notification) =>
            shown.push(
                notification.crashReport &&
                    crashReportText(notification.crashReport),
            ),
    );

    onCrash({ kind: 'restarted', report });
    const panic = { message: 'boom', stack: goStack('main.hover', 7) };
    onPanic(panic);
    onPanic(panic);

    await vi.waitFor(() =>
        expect(shown).toEqual([
            crashReportText({
                headline: 'panic: TODO - TraverseExpr',
                text: `The language server crashed and was restarted.\nBrowser: TestBrowser/1.0\n\n${report}`,
                permalink,
            }),
            crashReportText({
                headline: 'boom',
                text: `The language server hit an internal error and recovered.\nBrowser: TestBrowser/1.0\n\nboom\n\n${goStack('main.hover', 7).trim()}\n`,
                permalink,
            }),
        ]),
    );
});
