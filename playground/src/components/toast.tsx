import { type ReactNode, useEffect } from 'react';

import type { Notification } from '#editor/editor-state';
import styles from './toast.module.css';

const AUTO_DISMISS_MS = 4000;

type ToastProps = {
    type: Notification['type'];
    /** When true, the toast stays until it is dismissed instead of closing after four seconds. */
    persistent?: boolean;
    onDismiss: () => void;
    /** The toast's content: its message and any action buttons. */
    children: ReactNode;
};

/**
 * Shows children in a toast with a dismiss button. A non-persistent toast starts its
 * timer when it mounts, so give each notification its own `key`.
 */
export const Toast = ({
    type,
    persistent = false,
    onDismiss,
    children,
}: ToastProps) => {
    useEffect(() => {
        if (persistent) return;
        const timer = setTimeout(onDismiss, AUTO_DISMISS_MS);
        return () => clearTimeout(timer);
    }, [persistent, onDismiss]);

    return (
        <div className={`${styles.toast} ${styles[type]}`} role="alert">
            {children}
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

type ToastButtonProps = {
    onClick: () => void;
    children: ReactNode;
};

/** A button styled to sit inside a Toast. */
export const ToastButton = ({ onClick, children }: ToastButtonProps) => (
    <button type="button" className={styles.action} onClick={onClick}>
        {children}
    </button>
);
