import { useEffect, useRef } from 'react';

import styles from './confirm-dialog.module.css';

type ConfirmDialogProps = {
    title: string;
    message: string;
    confirmLabel?: string;
    cancelLabel?: string;
    destructive?: boolean;
    onConfirm: () => void;
    onCancel: () => void;
};

export const ConfirmDialog = ({
    title,
    message,
    confirmLabel = 'Confirm',
    cancelLabel = 'Cancel',
    destructive = false,
    onConfirm,
    onCancel,
}: ConfirmDialogProps) => {
    const cancelRef = useRef<HTMLButtonElement>(null);

    useEffect(() => {
        cancelRef.current?.focus();

        const handleKeyDown = (e: KeyboardEvent) => {
            if (e.key === 'Escape') {
                onCancel();
            }
        };
        document.addEventListener('keydown', handleKeyDown);
        return () => document.removeEventListener('keydown', handleKeyDown);
    }, [onCancel]);

    return (
        <div
            className={styles.overlay}
            onClick={onCancel}
            onKeyDown={(e) => {
                if (e.key === 'Enter' || e.key === ' ') onCancel();
            }}
            role="presentation"
        >
            <dialog
                className={styles.dialog}
                open
                aria-labelledby="confirm-dialog-title"
                onClick={(e) => e.stopPropagation()}
                onKeyDown={(e) => e.stopPropagation()}
            >
                <h2 id="confirm-dialog-title" className={styles.title}>
                    {title}
                </h2>
                <p className={styles.message}>{message}</p>
                <div className={styles.buttons}>
                    <button
                        type="button"
                        ref={cancelRef}
                        className={`${styles.button} ${styles.cancelButton}`}
                        onClick={onCancel}
                    >
                        {cancelLabel}
                    </button>
                    <button
                        type="button"
                        className={`${styles.button} ${destructive ? styles.destructiveButton : styles.confirmButton}`}
                        onClick={onConfirm}
                    >
                        {confirmLabel}
                    </button>
                </div>
            </dialog>
        </div>
    );
};
