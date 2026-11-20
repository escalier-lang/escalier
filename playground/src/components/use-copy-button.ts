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
    // Each press stores a new object, so the effect below runs again and the outcome of a
    // second press shows for the full COPY_FEEDBACK_MS even when it matches the first.
    const [outcome, setOutcome] = useState<{ state: CopyState }>({
        state: 'idle',
    });

    useEffect(() => {
        if (outcome.state === 'idle') return;
        const timer = setTimeout(
            () => setOutcome({ state: 'idle' }),
            COPY_FEEDBACK_MS,
        );
        return () => clearTimeout(timer);
    }, [outcome]);

    const copy = async () => {
        try {
            await navigator.clipboard.writeText(text);
            setOutcome({ state: 'copied' });
        } catch {
            // The clipboard can refuse, as on a page served over plain http.
            setOutcome({ state: 'failed' });
        }
    };

    return { label: copyLabels[outcome.state], copy };
}
