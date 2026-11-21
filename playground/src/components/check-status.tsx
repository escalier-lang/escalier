import { checkStatusText, useCheckStore } from '#diagnostics/check-monitor';

import styles from './check-status.module.css';

/** Shows how long the last check took, or how long a check has run past its budget. */
export const CheckStatusIndicator = () => {
    const status = useCheckStore((state) => state.status);
    return (
        <output className={styles.status} aria-live="polite">
            {checkStatusText(status)}
        </output>
    );
};
