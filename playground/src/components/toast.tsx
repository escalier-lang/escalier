import { useEffect } from 'react';

import type { Notification } from '../editor-state';
import styles from './toast.module.css';

const AUTO_DISMISS_MS = 4000;

type ToastProps = {
    notification: Notification | null;
    onDismiss: () => void;
    /** Called when the user asks for a crash toast's report. */
    onShowCrashReport?: () => void;
};

export const Toast = ({
    notification,
    onDismiss,
    onShowCrashReport,
}: ToastProps) => {
    const offersCrashReport = Boolean(
        notification?.crashReport && onShowCrashReport,
    );
    useEffect(() => {
        // A toast that offers a crash report stays until it is dismissed, so the user has
        // time to open the report.
        if (!notification || offersCrashReport) return;
        const timer = setTimeout(onDismiss, AUTO_DISMISS_MS);
        return () => clearTimeout(timer);
    }, [notification, offersCrashReport, onDismiss]);

    if (!notification) return null;

    return (
        <div
            className={`${styles.toast} ${styles[notification.type]}`}
            role="alert"
        >
            <span>{notification.message}</span>
            {offersCrashReport && (
                <button
                    type="button"
                    className={styles.details}
                    onClick={onShowCrashReport}
                >
                    Details
                </button>
            )}
            <button
                type="button"
                className={styles.dismiss}
                onClick={onDismiss}
                aria-label="Dismiss notification"
            >
                &times;
            </button>
        </div>
    );
};
