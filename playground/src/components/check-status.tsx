import { checkStatusText, useCheckStore } from '#diagnostics/check-monitor';

import styles from './check-status.module.css';

/** Shows in the toolbar how long the last check took, or that one is running long. */
export const CheckStatusIndicator = () => {
    const status = useCheckStore((state) => state.status);
    return (
        <output className={styles.status} aria-live="polite">
            {checkStatusText(status)}
        </output>
    );
};
