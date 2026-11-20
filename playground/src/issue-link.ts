/** The Escalier repository on GitHub. */
export const REPO_URL = 'https://github.com/escalier-lang/escalier';

/** Where an issue link opens a new issue. */
export const NEW_ISSUE_URL = `${REPO_URL}/issues/new`;

/**
 * The longest issue link the playground makes, counted after encoding. GitHub refuses a
 * URL much past 8 KB, so a longer report is cut short and the rest comes from the
 * clipboard.
 */
export const MAX_ISSUE_URL_CHARS = 7500;

/** The issue form in .github/ISSUE_TEMPLATE that bugFormUrl fills in. */
export const PLAYGROUND_BUG_FORM = 'playground-bug.yml';

/** The longest headline an issue title takes. */
const ISSUE_HEADLINE_CHARS = 120;

/** What a truncated field ends with, after the part of it the link holds. */
export const TRUNCATION_NOTE =
    '…\nThis was cut short to fit the issue link. Paste the rest from the report that Copy report in the playground puts on the clipboard.';

/** The fields of the playground bug form, keyed by their ids in playground-bug.yml. */
export type BugFormFields = {
    'what-happened'?: string;
    expected?: string;
    'playground-link'?: string;
    errors?: string;
    'crash-report'?: string;
    environment?: string;
};

/**
 * A way bugFormUrl may shrink one field when the filled-in form makes the link too long.
 * A truncate cut keeps the longest prefix of the field that fits, but no less than
 * `keep` characters, and ends it with TRUNCATION_NOTE. An omit cut replaces the field
 * with `note`.
 */
export type FieldCut =
    | { field: keyof BugFormFields; kind: 'truncate'; keep: number }
    | { field: keyof BugFormFields; kind: 'omit'; note: string };

/**
 * Returns `${prefix}: ${headline}`, with headline cut to ISSUE_HEADLINE_CHARS and ended
 * with an ellipsis when it is longer.
 */
export function issueTitle(prefix: string, headline: string): string {
    const shown =
        headline.length > ISSUE_HEADLINE_CHARS
            ? `${prefixOf(headline, ISSUE_HEADLINE_CHARS - 1)}…`
            : headline;
    return `${prefix}: ${shown}`;
}

/**
 * Returns the link that opens the playground bug form with title and fields filled in.
 * While the link is longer than MAX_ISSUE_URL_CHARS, it applies cuts in order, as
 * FieldCut describes, and stops once the link fits. Each field a truncate cut shortened
 * then takes back as much of its text as the remaining room allows. The link can still
 * be too long when the cuts run out.
 */
export function bugFormUrl(
    title: string,
    fields: BugFormFields,
    cuts: readonly FieldCut[] = [],
): string {
    const current = { ...fields };
    const applied: FieldCut[] = [];
    for (const cut of cuts) {
        if (formUrl(title, current).length <= MAX_ISSUE_URL_CHARS) {
            break;
        }
        applied.push(cut);
        current[cut.field] = applyCut(title, fields, current, cut);
    }
    // A later cut can free room that an earlier truncate cut needed, as when the
    // playground link is left out after the crash report was cut to its minimum.
    const omitted = new Set(
        applied.filter((cut) => cut.kind === 'omit').map((cut) => cut.field),
    );
    for (const cut of applied) {
        if (cut.kind === 'truncate' && !omitted.has(cut.field)) {
            current[cut.field] = applyCut(title, fields, current, cut);
        }
    }
    return formUrl(title, current);
}

/**
 * Returns the text cut gives its field, given the original fields and the current,
 * partly cut ones. A truncate cut measures against the current fields but cuts the
 * field's original text.
 */
function applyCut(
    title: string,
    fields: BugFormFields,
    current: BugFormFields,
    cut: FieldCut,
): string | undefined {
    const text = fields[cut.field];
    if (text === undefined) {
        return undefined;
    }
    if (cut.kind === 'omit') {
        return cut.note;
    }
    return longestFittingPrefix(title, current, cut.field, text, cut.keep);
}

/**
 * Returns field's text cut to the longest prefix, ended with TRUNCATION_NOTE, that keeps
 * the link within MAX_ISSUE_URL_CHARS, or to its first keep characters when no longer
 * prefix fits. It returns text whole when the link fits with all of it, or when text is
 * no longer than keep.
 */
function longestFittingPrefix(
    title: string,
    fields: BugFormFields,
    field: keyof BugFormFields,
    text: string,
    keep: number,
): string {
    const cutTo = (length: number) =>
        `${prefixOf(text, length)}\n${TRUNCATION_NOTE}`;
    const fits = (length: number) =>
        formUrl(title, { ...fields, [field]: cutTo(length) }).length <=
        MAX_ISSUE_URL_CHARS;
    // Encoding lengthens some characters more than others, so the longest prefix that
    // fits is found by measuring the encoded link.
    if (
        keep >= text.length ||
        formUrl(title, { ...fields, [field]: text }).length <=
            MAX_ISSUE_URL_CHARS
    ) {
        return text;
    }
    let fitting = keep;
    if (!fits(fitting)) {
        return cutTo(fitting);
    }
    let tooLong = text.length;
    while (tooLong - fitting > 1) {
        const mid = Math.floor((fitting + tooLong) / 2);
        if (fits(mid)) {
            fitting = mid;
        } else {
            tooLong = mid;
        }
    }
    return cutTo(fitting);
}

function formUrl(title: string, fields: BugFormFields): string {
    const params = new URLSearchParams({
        template: PLAYGROUND_BUG_FORM,
        title,
    });
    for (const [field, text] of Object.entries(fields)) {
        if (text !== undefined && text !== '') {
            params.set(field, text);
        }
    }
    return `${NEW_ISSUE_URL}?${params.toString()}`;
}

/**
 * Returns the first length UTF-16 code units of text, or one fewer when the cut would
 * split a surrogate pair. URLSearchParams encodes a lone surrogate as U+FFFD, so a split
 * pair would end the text with a replacement character.
 */
export function prefixOf(text: string, length: number): string {
    const last = text.charCodeAt(length - 1);
    const splitsPair = length < text.length && last >= 0xd800 && last <= 0xdbff;
    return text.slice(0, splitsPair ? length - 1 : length);
}
