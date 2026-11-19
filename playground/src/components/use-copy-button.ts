import { useEffect, useState } from 'react';

/** What the copy button says, which changes after the user presses it. */
type CopyState = 'idle' | 'copied' | 'failed';

/** How long the copy button shows the outcome of a copy before it reads as before. */
const COPY_FEEDBACK_MS = 2000;

const copyLabels: Record<CopyState, string> = {
    idle: 'Copy report',
    copied: 'Copied',
    failed: 'Copy failed. Select the text instead.',
};

/**
 * Returns the label of a button that copies text to the clipboard, and the function the
 * button calls. The label says whether the last copy worked for COPY_FEEDBACK_MS, then
 * reads "Copy report" again.
 */
export function useCopyButton(text: string): {
    label: string;
    copy: () => Promise<void>;
} {
    const [copyState, setCopyState] = useState<CopyState>('idle');

    // The outcome of a copy shows for a moment, so pressing the button again shows the
    // outcome of that copy.
    useEffect(() => {
        if (copyState === 'idle') return;
        const timer = setTimeout(() => setCopyState('idle'), COPY_FEEDBACK_MS);
        return () => clearTimeout(timer);
    }, [copyState]);

    const copy = async () => {
        try {
            await navigator.clipboard.writeText(text);
            setCopyState('copied');
        } catch {
            // The clipboard can refuse, as on a page served over plain http.
            setCopyState('failed');
        }
    };

    return { label: copyLabels[copyState], copy };
}
