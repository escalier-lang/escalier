import { useRef } from 'react';

import { useModalDialog } from './use-modal-dialog';

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
    const { dialogRef, onBackdropClick } = useModalDialog(onCancel, cancelRef);

    return (
        <dialog
            ref={dialogRef}
            className={styles.dialog}
            aria-labelledby="confirm-dialog-title"
            aria-describedby="confirm-dialog-message"
            aria-modal="true"
            onClick={onBackdropClick}
            // Satisfies jsx-a11y/click-events-have-key-events; keyboard
            // dismissal is handled by the native dialog cancel event.
            onKeyDown={() => {}}
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
