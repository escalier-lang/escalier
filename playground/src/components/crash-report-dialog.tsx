import { useEffect, useRef, useState } from 'react';

import { crashIssueUrl } from '../crash-notification';
import type { CrashReport } from '../editor-state';
import styles from './crash-report-dialog.module.css';
import { useModalDialog } from './use-modal-dialog';

type CrashReportDialogProps = {
    report: CrashReport;
    onClose: () => void;
};

/** What the copy button says, which changes after the user presses it. */
type CopyState = 'idle' | 'copied' | 'failed';

/** How long the copy button shows the outcome of a copy before it reads as before. */
const COPY_FEEDBACK_MS = 2000;

const copyLabels: Record<CopyState, string> = {
    idle: 'Copy report',
    copied: 'Copied',
    failed: 'Copy failed. Select the text instead.',
};

/**
 * Shows a language server crash report, with buttons that copy it and open a GitHub
 * issue for it.
 */
export const CrashReportDialog = ({
    report,
    onClose,
}: CrashReportDialogProps) => {
    const copyRef = useRef<HTMLButtonElement>(null);
    const { dialogRef, onBackdropClick } = useModalDialog(onClose, copyRef);
    const [copyState, setCopyState] = useState<CopyState>('idle');

    // The outcome of a copy shows for a moment, so pressing the button again shows the
    // outcome of that copy.
    useEffect(() => {
        if (copyState === 'idle') return;
        const timer = setTimeout(() => setCopyState('idle'), COPY_FEEDBACK_MS);
        return () => clearTimeout(timer);
    }, [copyState]);

    const copy = async () => {
        try {
            await navigator.clipboard.writeText(report.text);
            setCopyState('copied');
        } catch {
            // The clipboard can refuse, as on a page served over plain http.
            setCopyState('failed');
        }
    };

    return (
        <dialog
            ref={dialogRef}
            className={styles.dialog}
            aria-labelledby="crash-report-title"
            aria-describedby="crash-report-help"
            aria-modal="true"
            onClick={onBackdropClick}
            // Satisfies jsx-a11y/click-events-have-key-events; keyboard
            // dismissal is handled by the native dialog cancel event.
            onKeyDown={() => {}}
        >
            <h2 id="crash-report-title" className={styles.title}>
                Language server crash report
            </h2>
            <p id="crash-report-help" className={styles.message}>
                Copy this report into a GitHub issue, with the code that
                triggers the crash if you can share it.
            </p>
            <pre className={styles.report} aria-label="Crash report">
                {report.text}
            </pre>
            <div className={styles.buttons}>
                <button
                    type="button"
                    className={`${styles.button} ${styles.secondaryButton}`}
                    onClick={onClose}
                >
                    Close
                </button>
                <a
                    className={`${styles.button} ${styles.secondaryButton}`}
                    href={crashIssueUrl(report)}
                    target="_blank"
                    rel="noreferrer"
                >
                    Open GitHub issue
                </a>
                <button
                    type="button"
                    ref={copyRef}
                    className={`${styles.button} ${styles.primaryButton}`}
                    onClick={copy}
                >
                    {copyLabels[copyState]}
                </button>
            </div>
        </dialog>
    );
};
