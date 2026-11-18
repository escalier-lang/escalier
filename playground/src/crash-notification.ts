import type { CrashReport, Notification } from './editor-state';
import type { PanicReport, ServerCrashEvent } from './lsp-client/client';

/** Where the issue link in a crash report opens a new issue. */
export const NEW_ISSUE_URL =
    'https://github.com/escalier-lang/escalier/issues/new';

/**
 * The longest issue link crashIssueUrl returns, counted after encoding. GitHub refuses a
 * URL much past 8 KB, so a longer report is cut short and the rest comes from the
 * clipboard.
 */
export const MAX_ISSUE_URL_CHARS = 7500;

/** The longest headline an issue title takes from a crash report. */
const ISSUE_HEADLINE_CHARS = 120;

/**
 * Returns the toast that tells the user the language server crashed, carrying the crash
 * report. userAgent describes the browser, for the report.
 */
export function crashNotification(
    event: ServerCrashEvent,
    userAgent: string,
): Notification {
    switch (event.kind) {
        case 'restarted':
            return withReport(
                'The language server crashed and was restarted.',
                'warning',
                event.report,
                userAgent,
            );
        case 'waiting-for-edit':
            return withReport(
                'The language server crashed again on the same code. It will restart when you next edit a file.',
                'error',
                event.report,
                userAgent,
            );
    }
}

/**
 * Returns the toast that tells the user the language server recovered from a panic,
 * carrying the panic's message and stack. userAgent describes the browser, for the report.
 */
export function panicNotification(
    report: PanicReport,
    userAgent: string,
): Notification {
    return withReport(
        'The language server hit an internal error and recovered.',
        'warning',
        `${report.message}\n\n${report.stack}`,
        userAgent,
    );
}

/**
 * Returns a toast with message whose crash report holds details, the server's own account
 * of the crash. The report's text opens with message and the browser so that a pasted
 * copy stands on its own.
 */
function withReport(
    message: string,
    type: Notification['type'],
    details: string,
    userAgent: string,
): Notification {
    const headline = details.trim().split('\n', 1)[0] ?? '';
    const text = `${message}\nBrowser: ${userAgent}\n\n${details.trim()}\n`;
    return { message, type, crashReport: { headline, text } };
}

/**
 * Returns the link that opens a new GitHub issue for report, with its title and body
 * filled in. When the whole report would make the link longer than MAX_ISSUE_URL_CHARS,
 * the body holds as much of the report as fits and says to paste the rest.
 */
export function crashIssueUrl(report: CrashReport): string {
    let headline = report.headline;
    if (headline.length > ISSUE_HEADLINE_CHARS) {
        headline = `${headline.slice(0, ISSUE_HEADLINE_CHARS - 1)}…`;
    }
    const title = `Playground language server crash: ${headline}`;
    const text = report.text.trimEnd();
    const whole = issueUrl(title, text, false);
    if (whole.length <= MAX_ISSUE_URL_CHARS) {
        return whole;
    }
    // Encoding lengthens some characters more than others, so the longest prefix of the
    // report that fits is found by measuring the encoded link.
    let fits = 0;
    let tooLong = text.length;
    while (tooLong - fits > 1) {
        const mid = Math.floor((fits + tooLong) / 2);
        if (
            issueUrl(title, text.slice(0, mid), true).length <=
            MAX_ISSUE_URL_CHARS
        ) {
            fits = mid;
        } else {
            tooLong = mid;
        }
    }
    return issueUrl(title, text.slice(0, fits), true);
}

/**
 * Returns the new-issue link with title and a body that holds text. When truncated is
 * true, the body marks text as cut short.
 */
function issueUrl(title: string, text: string, truncated: boolean): string {
    const body = [
        '## What happened',
        '',
        '<!-- What were you doing when the language server crashed? Paste the code that triggers it if you can. -->',
        '',
        '## Crash report',
        '',
        ...(truncated
            ? [
                  'The report below is cut short. Paste the full report, which **Copy report** puts on the clipboard.',
                  '',
              ]
            : []),
        '```',
        truncated ? `${text}\n…` : text,
        '```',
        '',
    ].join('\n');
    const params = new URLSearchParams({ title, body });
    return `${NEW_ISSUE_URL}?${params.toString()}`;
}

/**
 * Returns a function that passes each panic report to show the first time its message
 * arrives, and drops later reports with the same message.
 */
export function firstOfEachPanic(
    show: (report: PanicReport) => void,
): (report: PanicReport) => void {
    const seen = new Set<string>();
    return (report) => {
        if (seen.has(report.message)) {
            return;
        }
        seen.add(report.message);
        show(report);
    };
}
