import { useRef } from 'react';

import { crashIssueUrl, crashReportText } from '../crash-notification';
import type { CrashReport } from '../editor-state';
import styles from './crash-report-dialog.module.css';
import { useCopyButton } from './use-copy-button';
import { useModalDialog } from './use-modal-dialog';

type CrashReportDialogProps = {
    report: CrashReport;
    onClose: () => void;
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
    const text = crashReportText(report);
    const { label: copyLabel, copy } = useCopyButton(text);

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
                {report.permalink === undefined
                    ? 'Copy this report into a GitHub issue, with the code that triggers the crash if you can share it.'
                    : 'Copy this report into a GitHub issue. Its playground link opens the project as it was when the server crashed.'}
            </p>
            <pre className={styles.report} aria-label="Crash report">
                {text}
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
                    {copyLabel}
                </button>
            </div>
        </dialog>
    );
};
