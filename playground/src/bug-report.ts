import {
    type BugFormFields,
    type FieldCut,
    bugFormUrl,
    issueTitle,
} from './issue-link';

/** An error the editor shows in an open file. */
export type ShownError = {
    path: string;
    line: number;
    column: number;
    message: string;
};

/** What a bug report from the playground holds. */
export type BugReport = {
    whatHappened: string;
    expected: string;
    /**
     * A permalink to the project, or a sentence saying why the report has none. It is
     * undefined when the user chose to leave the link out.
     */
    playgroundLink?: string;
    errors: readonly ShownError[];
    /** The text of the latest crash report this session, if the server crashed. */
    crashReport?: string;
    /** The browser and the playground build, as environmentText writes them. */
    environment: string;
};

/** What the playground link field says when the issue link has no room for the link. */
export const LINK_LEFT_OUT_NOTE =
    'The link to the project is too long for this form. Paste it from the report that Copy report in the playground puts on the clipboard.';

/**
 * The order in which bugReportUrl shrinks the report's fields until the issue link fits.
 * The crash report and errors keep a useful part beside the playground link first. The
 * link goes next, since it is the longest field and the copied report still holds it.
 * Then the crash report, the errors and the user's own text give up the rest.
 */
const BUG_REPORT_CUTS: readonly FieldCut[] = [
    { field: 'crash-report', kind: 'truncate', keep: 1500 },
    { field: 'errors', kind: 'truncate', keep: 1000 },
    { field: 'playground-link', kind: 'omit', note: LINK_LEFT_OUT_NOTE },
    { field: 'crash-report', kind: 'truncate', keep: 0 },
    { field: 'errors', kind: 'truncate', keep: 0 },
    { field: 'what-happened', kind: 'truncate', keep: 500 },
    { field: 'expected', kind: 'truncate', keep: 500 },
];

/** Returns the environment section of a report for a browser and a playground build. */
export function environmentText(
    userAgent: string,
    commit: string | undefined,
): string {
    return `Browser: ${userAgent}\nPlayground build: ${commit ?? 'unknown'}`;
}

/**
 * Returns errors as `path:line:column: message` lines, sorted by path, line and column,
 * or an empty string when there are none.
 */
export function errorsText(errors: readonly ShownError[]): string {
    return [...errors]
        .sort(
            (a, b) =>
                a.path.localeCompare(b.path) ||
                a.line - b.line ||
                a.column - b.column,
        )
        .map((e) => `${e.path}:${e.line}:${e.column}: ${e.message}`)
        .join('\n');
}

/** Returns report as the fields of the playground bug form. */
export function bugReportFields(report: BugReport): BugFormFields {
    return {
        'what-happened': report.whatHappened.trim(),
        expected: report.expected.trim(),
        'playground-link': report.playgroundLink,
        errors: errorsText(report.errors),
        'crash-report': report.crashReport?.trim(),
        environment: report.environment,
    };
}

/**
 * Returns the link that opens the playground bug form filled in with report, shrunk to
 * fit as BUG_REPORT_CUTS orders. The title is the first line of what happened.
 */
export function bugReportUrl(report: BugReport): string {
    const headline =
        report.whatHappened.trim().split('\n', 1)[0] || 'Bug report';
    return bugFormUrl(
        issueTitle('Playground', headline),
        bugReportFields(report),
        BUG_REPORT_CUTS,
    );
}

/**
 * Returns report as Markdown with a section for each field that holds text, the same
 * sections the issue form shows, for the user to paste whole.
 */
export function bugReportText(report: BugReport): string {
    const fields = bugReportFields(report);
    const sections: [string, string | undefined, boolean][] = [
        ['What happened', fields['what-happened'], false],
        ['What you expected', fields.expected, false],
        ['Playground link', fields['playground-link'], false],
        ['Errors shown', fields.errors, true],
        ['Crash report', fields['crash-report'], true],
        ['Environment', fields.environment, true],
    ];
    return sections
        .filter(([, text]) => text)
        .map(([heading, text, code]) =>
            code
                ? `### ${heading}\n\n\`\`\`text\n${text}\n\`\`\``
                : `### ${heading}\n\n${text}`,
        )
        .join('\n\n')
        .concat('\n');
}
