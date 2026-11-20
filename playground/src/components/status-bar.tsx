import { errorCountLabel } from '../project-errors';
import styles from './status-bar.module.css';

type StatusBarProps = {
    /** How many errors the language server reports across the project. */
    errorCount: number;
    /** Called when the user asks to see the list of errors. */
    onShowErrors: () => void;
};

/** The bar along the bottom of the playground, which shows the project's error count. */
export const StatusBar = ({ errorCount, onShowErrors }: StatusBarProps) => (
    <footer className={styles.statusBar}>
        <button
            type="button"
            className={`${styles.item} ${errorCount > 0 ? styles.hasErrors : ''}`}
            onClick={onShowErrors}
            title="Show all errors"
        >
            {errorCountLabel(errorCount)}
        </button>
    </footer>
);
