import { useRef } from 'react';

import styles from './confirm-dialog.module.css';
import { useModalDialog } from './use-modal-dialog';

type MessageDialogProps = {
    title: string;
    message: string;
    /** Called when the user closes the dialog, with its button, Escape or the backdrop. */
    onClose: () => void;
};

/** Shows title and message in a modal dialog with a single button that closes it. */
export const MessageDialog = ({
    title,
    message,
    onClose,
}: MessageDialogProps) => {
    const closeRef = useRef<HTMLButtonElement>(null);
    const { dialogRef, onBackdropClick } = useModalDialog(onClose, closeRef);

    return (
        <dialog
            ref={dialogRef}
            className={styles.dialog}
            aria-labelledby="message-dialog-title"
            aria-describedby="message-dialog-message"
            aria-modal="true"
            onClick={onBackdropClick}
            // Satisfies jsx-a11y/click-events-have-key-events; keyboard
            // dismissal is handled by the native dialog cancel event.
            onKeyDown={() => {}}
        >
            <h2 id="message-dialog-title" className={styles.title}>
                {title}
            </h2>
            <p id="message-dialog-message" className={styles.message}>
                {message}
            </p>
            <div className={styles.buttons}>
                <button
                    type="button"
                    ref={closeRef}
                    className={`${styles.button} ${styles.confirmButton}`}
                    onClick={onClose}
                >
                    OK
                </button>
            </div>
        </dialog>
    );
};
