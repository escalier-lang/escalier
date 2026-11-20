import { useEffect, useRef, useState } from 'react';

import {
    type BugReport,
    type ShownError,
    bugReportText,
    bugReportUrl,
} from '../bug-report/bug-report';
import { MAX_PERMALINK_CHARS } from '../project/permalink';
import styles from './bug-report-dialog.module.css';
import dialogStyles from './crash-report-dialog.module.css';
import { useCopyButton } from './use-copy-button';
import { useModalDialog } from './use-modal-dialog';

type BugReportDialogProps = {
    /** Returns a permalink to the project as it is now. */
    capturePermalink: () => Promise<string>;
    /** The errors the editor shows in the open files. */
    errors: readonly ShownError[];
    /** The text of the latest crash report this session, if the server crashed. */
    crashReport?: string;
    /** The browser and the playground build, as environmentText writes them. */
    environment: string;
    onClose: () => void;
};

/** Where the dialog's permalink to the project stands. */
type LinkState =
    | { kind: 'pending' }
    | { kind: 'ready'; url: string }
    | { kind: 'too-long' }
    | { kind: 'failed' };

/**
 * Asks the user to describe a bug, then opens the playground bug form on GitHub filled
 * in with the description, a permalink to the project, the errors shown, the latest
 * crash report and the environment. A button copies the same report as Markdown. Once
 * the user opens the form, the dialog says that it opened and offers to open it again.
 */
export const BugReportDialog = ({
    capturePermalink,
    errors,
    crashReport,
    environment,
    onClose,
}: BugReportDialogProps) => {
    const describeRef = useRef<HTMLTextAreaElement>(null);
    const { dialogRef, onBackdropClick } = useModalDialog(onClose, describeRef);
    const [whatHappened, setWhatHappened] = useState('');
    const [expected, setExpected] = useState('');
    const [includeLink, setIncludeLink] = useState(true);
    const [link, setLink] = useState<LinkState>({ kind: 'pending' });
    // Whether the user has opened the issue form. The browser opens it in a new tab and
    // leaves this one as it was, so the dialog then says the form opened.
    const [issueOpened, setIssueOpened] = useState(false);
    const doneRef = useRef<HTMLButtonElement>(null);
    useEffect(() => {
        if (issueOpened) doneRef.current?.focus();
    }, [issueOpened]);

    // The link captures the project as it is when the dialog opens, which is the code
    // the user is reporting on.
    useEffect(() => {
        let cancelled = false;
        capturePermalink().then(
            (url) => {
                if (cancelled) return;
                setLink(
                    url.length > MAX_PERMALINK_CHARS
                        ? { kind: 'too-long' }
                        : { kind: 'ready', url },
                );
            },
            (err) => {
                console.error(
                    'Failed to link to the project for a bug report:',
                    err,
                );
                if (!cancelled) setLink({ kind: 'failed' });
            },
        );
        return () => {
            cancelled = true;
        };
    }, [capturePermalink]);

    const report: BugReport = {
        whatHappened,
        expected,
        playgroundLink: includeLink ? playgroundLinkText(link) : undefined,
        errors,
        crashReport,
        environment,
    };
    const { label: copyLabel, copy } = useCopyButton(bugReportText(report));
    const included = [
        errors.length === 1
            ? '1 error shown in the open files'
            : `${errors.length} errors shown in the open files`,
        ...(crashReport ? ['the latest crash report'] : []),
        'your browser and the playground build',
    ];

    const issueUrl = bugReportUrl(report);

    return (
        <dialog
            ref={dialogRef}
            className={dialogStyles.dialog}
            aria-labelledby="bug-report-title"
            aria-describedby="bug-report-help"
            aria-modal="true"
            onClick={onBackdropClick}
            // Satisfies jsx-a11y/click-events-have-key-events; keyboard
            // dismissal is handled by the native dialog cancel event.
            onKeyDown={() => {}}
        >
            {issueOpened ? (
                <>
                    <h2 id="bug-report-title" className={dialogStyles.title}>
                        Issue form opened
                    </h2>
                    <p id="bug-report-help" className={dialogStyles.message}>
                        The GitHub issue form opened in a new tab with your
                        report filled in. Check it there and submit it to file
                        the bug.
                    </p>
                    <p className={dialogStyles.message}>
                        If the tab didn't open, open the form again or copy the
                        report into a new issue.
                    </p>
                    <div className={dialogStyles.buttons}>
                        <button
                            type="button"
                            className={`${dialogStyles.button} ${dialogStyles.secondaryButton}`}
                            onClick={copy}
                        >
                            {copyLabel}
                        </button>
                        <a
                            className={`${dialogStyles.button} ${dialogStyles.secondaryButton}`}
                            href={issueUrl}
                            target="_blank"
                            rel="noreferrer"
                        >
                            Open the form again
                        </a>
                        <button
                            type="button"
                            ref={doneRef}
                            className={`${dialogStyles.button} ${dialogStyles.primaryButton}`}
                            onClick={onClose}
                        >
                            Done
                        </button>
                    </div>
                </>
            ) : (
                <>
                    <h2 id="bug-report-title" className={dialogStyles.title}>
                        Report a bug
                    </h2>
                    <p id="bug-report-help" className={dialogStyles.message}>
                        This opens a GitHub issue with the details below filled
                        in.
                    </p>
                    <label className={styles.field}>
                        What happened
                        <textarea
                            ref={describeRef}
                            className={styles.text}
                            rows={4}
                            value={whatHappened}
                            onChange={(e) => setWhatHappened(e.target.value)}
                        />
                    </label>
                    <label className={styles.field}>
                        What you expected
                        <textarea
                            className={styles.text}
                            rows={2}
                            value={expected}
                            onChange={(e) => setExpected(e.target.value)}
                        />
                    </label>
                    <label className={styles.check}>
                        <input
                            type="checkbox"
                            checked={includeLink}
                            onChange={(e) => setIncludeLink(e.target.checked)}
                        />
                        Include a link to this project
                    </label>
                    {includeLink && link.kind !== 'ready' && (
                        <p className={styles.status}>{linkStatus(link)}</p>
                    )}
                    <p className={dialogStyles.message}>
                        The report also holds {listOf(included)}.
                    </p>
                    <div className={dialogStyles.buttons}>
                        <button
                            type="button"
                            className={`${dialogStyles.button} ${dialogStyles.secondaryButton}`}
                            onClick={onClose}
                        >
                            Cancel
                        </button>
                        <button
                            type="button"
                            className={`${dialogStyles.button} ${dialogStyles.secondaryButton}`}
                            onClick={copy}
                            // A report copied now would leave out the link the user asked for.
                            disabled={includeLink && link.kind === 'pending'}
                        >
                            {copyLabel}
                        </button>
                        {includeLink && link.kind === 'pending' ? (
                            <button
                                type="button"
                                className={`${dialogStyles.button} ${dialogStyles.primaryButton}`}
                                disabled
                            >
                                Open GitHub issue
                            </button>
                        ) : (
                            <a
                                className={`${dialogStyles.button} ${dialogStyles.primaryButton}`}
                                href={issueUrl}
                                target="_blank"
                                rel="noreferrer"
                                onClick={() => setIssueOpened(true)}
                            >
                                Open GitHub issue
                            </a>
                        )}
                    </div>
                </>
            )}
        </dialog>
    );
};

/** Returns what the report's playground link field holds for link. */
function playgroundLinkText(link: LinkState): string | undefined {
    switch (link.kind) {
        case 'pending':
            return undefined;
        case 'ready':
            return link.url;
        case 'too-long':
            return 'None. The project is too large to link to.';
        case 'failed':
            return 'None. The playground could not make a link.';
    }
}

/** Returns what the dialog says about a link that isn't ready to include. */
function linkStatus(link: Exclude<LinkState, { kind: 'ready' }>): string {
    switch (link.kind) {
        case 'pending':
            return 'Making a link to the project…';
        case 'too-long':
            return `The project is too large to link to. A link can be at most ${MAX_PERMALINK_CHARS.toLocaleString('en-US')} characters.`;
        case 'failed':
            return 'The playground could not make a link to the project.';
    }
}

/** Returns items joined as an English list, such as "a, b and c". */
function listOf(items: readonly string[]): string {
    return items.length <= 1
        ? (items[0] ?? '')
        : `${items.slice(0, -1).join(', ')} and ${items.at(-1)}`;
}
