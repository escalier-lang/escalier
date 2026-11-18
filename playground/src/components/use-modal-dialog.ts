import { type MouseEvent, type RefObject, useEffect, useRef } from 'react';

/**
 * Shows the dialog that the returned dialogRef is attached to as a modal while the
 * component is mounted. onCancel runs when the user presses Escape or clicks the backdrop,
 * which the returned onBackdropClick handles once it is passed to the dialog's onClick.
 * focusRef, when given, is focused once the dialog opens.
 */
export function useModalDialog(
    onCancel: () => void,
    focusRef?: RefObject<HTMLElement | null>,
) {
    const dialogRef = useRef<HTMLDialogElement>(null);
    const onCancelRef = useRef(onCancel);
    onCancelRef.current = onCancel;

    useEffect(() => {
        const dialog = dialogRef.current;
        if (!dialog) return;

        dialog.showModal();
        focusRef?.current?.focus();

        const handleCancel = (e: Event) => {
            e.preventDefault();
            onCancelRef.current();
        };
        dialog.addEventListener('cancel', handleCancel);

        return () => {
            dialog.removeEventListener('cancel', handleCancel);
            dialog.close();
        };
    }, [focusRef]);

    const onBackdropClick = (e: MouseEvent<HTMLDialogElement>) => {
        // A click on the backdrop lands on the dialog element itself.
        if (e.target === dialogRef.current) {
            onCancelRef.current();
        }
    };

    return { dialogRef, onBackdropClick };
}
