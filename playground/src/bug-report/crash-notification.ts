import type { CrashReport, Notification } from '#editor/editor-state';
import type { Client, PanicReport, ServerCrashEvent } from '#lsp-client/client';
import { MAX_PERMALINK_CHARS } from '#project/permalink';

import { LINK_LEFT_OUT_NOTE } from './bug-report';
import { type FieldCut, bugFormUrl, issueTitle } from './issue-link';

/**
 * The order in which crashIssueUrl shrinks the form's fields until the issue link fits.
 * The report keeps its first 1500 characters beside the playground link. When the link
 * leaves less room than that, the link is left out, since the copied report holds it,
 * and the report takes the room.
 */
const CRASH_ISSUE_CUTS: readonly FieldCut[] = [
    { field: 'crash-report', kind: 'truncate', keep: 1500 },
    { field: 'playground-link', kind: 'omit', note: LINK_LEFT_OUT_NOTE },
    { field: 'crash-report', kind: 'truncate', keep: 0 },
];

/**
 * Returns the toast that tells the user the language server crashed, carrying the crash
 * report. userAgent describes the browser, for the report. permalink links to the
 * project, or is undefined when the playground couldn't make a link.
 */
export function crashNotification(
    event: ServerCrashEvent,
    userAgent: string,
    permalink?: string,
): Notification {
    switch (event.kind) {
        case 'restarted':
            return withReport(
                'The language server crashed and was restarted.',
                'warning',
                event.report,
                userAgent,
                permalink,
            );
        case 'waiting-for-edit':
            return withReport(
                'The language server crashed again on the same code. It will restart when you next edit a file.',
                'error',
                event.report,
                userAgent,
                permalink,
            );
    }
}

/**
 * Returns the toast that tells the user the language server recovered from a panic,
 * carrying the panic's message and stack. userAgent describes the browser, for the report.
 * permalink links to the project, or is undefined when the playground couldn't make a
 * link.
 */
export function panicNotification(
    report: PanicReport,
    userAgent: string,
    permalink?: string,
): Notification {
    return withReport(
        'The language server hit an internal error and recovered.',
        'warning',
        `${report.message}\n\n${report.stack}`,
        userAgent,
        permalink,
    );
}

/**
 * Returns a toast with message whose crash report holds details, the server's own account
 * of the crash. The report's text opens with message and the browser so that a pasted
 * copy stands on its own. The report carries permalink when it is at most
 * MAX_PERMALINK_CHARS long, and its text says the project is too large to link to when
 * permalink is longer.
 */
function withReport(
    message: string,
    type: Notification['type'],
    details: string,
    userAgent: string,
    permalink: string | undefined,
): Notification {
    const headline = details.trim().split('\n', 1)[0] ?? '';
    const tooLong =
        permalink !== undefined && permalink.length > MAX_PERMALINK_CHARS;
    const linkNote = tooLong
        ? 'Playground link: none, because the project is too large to link to.\n'
        : '';
    const text = `${message}\nBrowser: ${userAgent}\n${linkNote}\n${details.trim()}\n`;
    const crashReport: CrashReport = { headline, text };
    if (permalink !== undefined && !tooLong) {
        crashReport.permalink = permalink;
    }
    return { message, type, crashReport };
}

/** Returns the text of report that the user reads and copies, with its playground link. */
export function crashReportText(report: CrashReport): string {
    return report.permalink === undefined
        ? report.text
        : `${report.text}\nPlayground link: ${report.permalink}\n`;
}

/**
 * Returns the link that opens the playground bug form for report, with the report, its
 * playground link and environment filled in. environment describes the browser and the
 * playground build, as environmentText writes it.
 */
export function crashIssueUrl(
    report: CrashReport,
    environment: string,
): string {
    return bugFormUrl(
        issueTitle('Playground language server crash', report.headline),
        {
            'crash-report': report.text.trimEnd(),
            'playground-link': report.permalink,
            environment,
        },
        CRASH_ISSUE_CUTS,
    );
}

/**
 * Returns a function that shows the crash toast its argument builds from a permalink.
 * Each call starts capture at once, and the toast shows after the toasts of earlier
 * calls, so toasts show in the order of the calls. When capture rejects, the toast
 * gets undefined in place of a permalink. A toast that throws while it is built or
 * shown is logged, and later toasts still show.
 */
export function crashToasts(
    capture: () => Promise<string>,
    show: (notification: Notification) => void,
): (toast: (permalink: string | undefined) => Notification) => void {
    let lastShown = Promise.resolve();
    return (toast) => {
        const permalink = capture().catch((err) => {
            console.error(
                'Failed to link to the project for a crash report:',
                err,
            );
            return undefined;
        });
        // A toast that fails to build or show is logged and skipped, so the next
        // toast still waits on a settled promise rather than a rejected one.
        lastShown = lastShown
            .then(async () => show(toast(await permalink)))
            .catch((err) => {
                console.error('Failed to show a crash toast:', err);
            });
    };
}

/**
 * Shows a toast for each crash and panic that client reports. Each toast carries a
 * crash report whose permalink comes from capture, as crashToasts describes, and
 * userAgent describes the browser, for the report. A panic shows once however often
 * it recurs, as firstOfEachPanic describes.
 */
export function showServerCrashes(
    client: Pick<Client, 'onServerCrash' | 'onServerPanic'>,
    userAgent: string,
    capture: () => Promise<string>,
    show: (notification: Notification) => void,
): void {
    const showCrashToast = crashToasts(capture, show);
    client.onServerCrash((event) =>
        showCrashToast((permalink) =>
            crashNotification(event, userAgent, permalink),
        ),
    );
    // A panic in a request handler recurs on every request that reaches the same
    // code, such as each hover as the mouse moves. Its toast would come back each time
    // the user dismissed it, so each panic is shown once.
    client.onServerPanic(
        firstOfEachPanic((report) =>
            showCrashToast((permalink) =>
                panicNotification(report, userAgent, permalink),
            ),
        ),
    );
}

/** How many distinct panics firstOfEachPanic remembers before it starts over. */
const SEEN_PANICS_LIMIT = 100;

/**
 * Returns a function that passes each panic report to show the first time its panic
 * arrives, and drops later reports of the same panic. Two reports are the same panic when
 * they have the same message and panicked at the same place, which is the stack frame
 * below the `panic(` frame. After SEEN_PANICS_LIMIT distinct panics it forgets them all, so
 * a long session's memory of them stays bounded.
 */
export function firstOfEachPanic(
    show: (report: PanicReport) => void,
): (report: PanicReport) => void {
    const seen = new Set<string>();
    return (report) => {
        const key = `${report.message}\n${panicSite(report.stack)}`;
        if (seen.has(key)) {
            return;
        }
        if (seen.size >= SEEN_PANICS_LIMIT) {
            seen.clear();
        }
        seen.add(key);
        show(report);
    };
}

/**
 * Returns the function that panicked in a Go stack trace: the line after the `panic(`
 * frame. It returns the whole stack when the trace has no such frame.
 */
function panicSite(stack: string): string {
    const lines = stack.split('\n');
    const panicFrame = lines.findIndex((line) => line.startsWith('panic('));
    if (panicFrame === -1) {
        return stack;
    }
    // Each frame is a function line followed by an indented file:line line.
    return lines[panicFrame + 2] ?? stack;
}
