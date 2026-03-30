import { useEffect } from 'react';

import { usePlaygroundDispatch, usePlaygroundState } from '../state';
import styles from './toast.module.css';

const AUTO_DISMISS_MS = 4000;

export const Toast = () => {
    const { notification } = usePlaygroundState();
    const dispatch = usePlaygroundDispatch();

    useEffect(() => {
        if (!notification) return;
        const timer = setTimeout(
            () => dispatch({ type: 'dismissNotification' }),
            AUTO_DISMISS_MS,
        );
        return () => clearTimeout(timer);
    }, [notification, dispatch]);

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
                onClick={() => dispatch({ type: 'dismissNotification' })}
                aria-label="Dismiss notification"
            >
                &times;
            </button>
        </div>
    );
};
