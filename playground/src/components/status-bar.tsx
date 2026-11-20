import { type DiagnosticCounts, countOf } from '../project-diagnostics';
import styles from './status-bar.module.css';

type StatusBarProps = {
    /** How many errors and warnings the language server reports across the project. */
    counts: DiagnosticCounts;
    /** Called when the user asks to see the list of diagnostics. */
    onShowDiagnostics: () => void;
};

/** A circle with a cross in it, the error icon. */
const ErrorIcon = () => (
    <svg width="14" height="14" viewBox="0 0 16 16" aria-hidden="true">
        <circle cx="8" cy="8" r="6.5" fill="none" stroke="currentColor" />
        <path
            d="M5.5 5.5l5 5m0-5l-5 5"
            stroke="currentColor"
            strokeWidth="1.5"
            strokeLinecap="round"
        />
    </svg>
);

/** A triangle with an exclamation mark in it, the warning icon. */
const WarningIcon = () => (
    <svg width="14" height="14" viewBox="0 0 16 16" aria-hidden="true">
        <path
            d="M8 1.75l6.5 12H1.5z"
            fill="none"
            stroke="currentColor"
            strokeLinejoin="round"
        />
        <path
            d="M8 6v3.5"
            stroke="currentColor"
            strokeWidth="1.5"
            strokeLinecap="round"
        />
        <circle cx="8" cy="11.75" r="0.9" fill="currentColor" />
    </svg>
);

/**
 * The bar along the bottom of the playground. It shows the project's error and warning
 * counts, which open the diagnostics tab when clicked.
 */
export const StatusBar = ({ counts, onShowDiagnostics }: StatusBarProps) => (
    <footer className={styles.statusBar}>
        <button
            type="button"
            className={styles.item}
            onClick={onShowDiagnostics}
            aria-label={`${countOf(counts.errors, 'error')}, ${countOf(counts.warnings, 'warning')}`}
            title="Show all diagnostics"
        >
            <span
                className={`${styles.count} ${counts.errors > 0 ? styles.hasErrors : ''}`}
            >
                <ErrorIcon />
                {counts.errors}
            </span>
            <span
                className={`${styles.count} ${counts.warnings > 0 ? styles.hasWarnings : ''}`}
            >
                <WarningIcon />
                {counts.warnings}
            </span>
        </button>
    </footer>
);
