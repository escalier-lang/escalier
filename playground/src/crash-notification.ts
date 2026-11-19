import type { CrashReport, Notification } from './editor-state';
import type {
    Client,
    PanicReport,
    ServerCrashEvent,
} from './lsp-client/client';
import { MAX_PERMALINK_CHARS } from './permalink';

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
 * The least of the report an issue link holds beside the playground link. When the
 * playground link leaves less room than this, the issue link leaves it out and holds
 * more of the report.
 */
const MIN_ISSUE_REPORT_CHARS = 1500;

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
 * Returns the link that opens a new GitHub issue for report, with its title and body
 * filled in. The body holds the report's playground link when the link leaves room for
 * at least MIN_ISSUE_REPORT_CHARS of the report, and otherwise says to paste the link.
 * When the whole report would make the link longer than MAX_ISSUE_URL_CHARS, the body
 * holds as much of the report as fits and says to paste the rest.
 */
export function crashIssueUrl(report: CrashReport): string {
    let headline = report.headline;
    if (headline.length > ISSUE_HEADLINE_CHARS) {
        headline = `${prefixOf(headline, ISSUE_HEADLINE_CHARS - 1)}…`;
    }
    const title = `Playground language server crash: ${headline}`;
    const text = report.text.trimEnd();
    if (report.permalink === undefined) {
        return fittedIssueUrl(title, text, { kind: 'none' }).url;
    }
    const withLink = fittedIssueUrl(title, text, {
        kind: 'link',
        url: report.permalink,
    });
    if (withLink.reportChars >= Math.min(MIN_ISSUE_REPORT_CHARS, text.length)) {
        return withLink.url;
    }
    return fittedIssueUrl(title, text, { kind: 'omitted' }).url;
}

/**
 * What an issue body says about the playground link: nothing, when the report has
 * none; the link itself; or that the link was left out and is in the copied report.
 */
type IssueLink =
    | { kind: 'none' }
    | { kind: 'link'; url: string }
    | { kind: 'omitted' };

/**
 * Returns the new-issue link with title, link and the longest prefix of text that keeps
 * it within MAX_ISSUE_URL_CHARS, along with how many characters of text it holds.
 */
function fittedIssueUrl(
    title: string,
    text: string,
    link: IssueLink,
): { url: string; reportChars: number } {
    const whole = issueUrl(title, text, false, link);
    if (whole.length <= MAX_ISSUE_URL_CHARS) {
        return { url: whole, reportChars: text.length };
    }
    // Encoding lengthens some characters more than others, so the longest prefix of the
    // report that fits is found by measuring the encoded link.
    let fits = 0;
    let tooLong = text.length;
    while (tooLong - fits > 1) {
        const mid = Math.floor((fits + tooLong) / 2);
        if (
            issueUrl(title, prefixOf(text, mid), true, link).length <=
            MAX_ISSUE_URL_CHARS
        ) {
            fits = mid;
        } else {
            tooLong = mid;
        }
    }
    const kept = prefixOf(text, fits);
    return { url: issueUrl(title, kept, true, link), reportChars: kept.length };
}

/**
 * Returns the first length UTF-16 code units of text, or one fewer when the cut would
 * split a surrogate pair. URLSearchParams encodes a lone surrogate as U+FFFD, so a split
 * pair would end the issue body with a replacement character.
 */
function prefixOf(text: string, length: number): string {
    const last = text.charCodeAt(length - 1);
    const splitsPair = length < text.length && last >= 0xd800 && last <= 0xdbff;
    return text.slice(0, splitsPair ? length - 1 : length);
}

/**
 * Returns the new-issue link with title and a body that holds text and link. When
 * truncated is true, the body marks text as cut short.
 */
function issueUrl(
    title: string,
    text: string,
    truncated: boolean,
    link: IssueLink,
): string {
    const body = [
        '## What happened',
        '',
        '<!-- What were you doing when the language server crashed? Paste the code that triggers it if you can. -->',
        '',
        ...issueLinkSection(link),
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

/** Returns the lines of an issue body's section about the playground link. */
function issueLinkSection(link: IssueLink): string[] {
    switch (link.kind) {
        case 'none':
            return [];
        case 'link':
            return ['## Playground link', '', link.url, ''];
        case 'omitted':
            return [
                '## Playground link',
                '',
                'The link to the project is too long for this form. Paste it from the full report, which **Copy report** puts on the clipboard.',
                '',
            ];
    }
}

/**
 * Returns a function that shows the crash toast its argument builds from a permalink.
 * Each call starts capture at once, and the toast shows after the toasts of earlier
 * calls, so toasts show in the order of the calls. When capture rejects, the toast
 * gets undefined in place of a permalink.
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
        lastShown = lastShown.then(async () => show(toast(await permalink)));
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
