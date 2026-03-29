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
    const dialogRef = useRef<HTMLDialogElement>(null);
    const cancelRef = useRef<HTMLButtonElement>(null);
    const onCancelRef = useRef(onCancel);
    onCancelRef.current = onCancel;

    useEffect(() => {
        const dialog = dialogRef.current;
        if (!dialog) return;

        dialog.showModal();
        cancelRef.current?.focus();

        const handleCancel = (e: Event) => {
            e.preventDefault();
            onCancelRef.current();
        };
        dialog.addEventListener('cancel', handleCancel);

        return () => {
            dialog.removeEventListener('cancel', handleCancel);
            dialog.close();
        };
    }, []);

    return (
        <dialog
            ref={dialogRef}
            className={styles.dialog}
            aria-labelledby="confirm-dialog-title"
            aria-describedby="confirm-dialog-message"
            aria-modal="true"
            onClick={(e) => {
                // Close when clicking the backdrop (the dialog element itself)
                if (e.target === dialogRef.current) {
                    onCancel();
                }
            }}
            onKeyDown={() => {
                // Keyboard dismissal handled by the native dialog cancel event
            }}
        >
            <h2 id="confirm-dialog-title" className={styles.title}>
                {title}
            </h2>
            <p id="confirm-dialog-message" className={styles.message}>
                {message}
            </p>
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
    );
};
