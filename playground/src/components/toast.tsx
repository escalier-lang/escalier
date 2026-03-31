import { useEffect } from 'react';

import type { Notification } from '../editor-state';
import styles from './toast.module.css';

const AUTO_DISMISS_MS = 4000;

type ToastProps = {
    notification: Notification | null;
    onDismiss: () => void;
};

export const Toast = ({ notification, onDismiss }: ToastProps) => {
    useEffect(() => {
        if (!notification) return;
        const timer = setTimeout(onDismiss, AUTO_DISMISS_MS);
        return () => clearTimeout(timer);
    }, [notification, onDismiss]);

    if (!notification) return null;

    return (
        <div
            className={`${styles.toast} ${styles[notification.type]}`}
            role="alert"
        >
            <span>{notification.message}</span>
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
