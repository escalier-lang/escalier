import { useCallback, useState } from 'react';

import { REPO_URL } from '#bug-report/issue-link';
import {
    type ThemePreference,
    themePreferences,
    useThemeStore,
} from '#theme/theme';

import { CheckStatusIndicator } from './check-status';
import { ConfirmDialog } from './confirm-dialog';
import { Dropdown, type DropdownItem } from './dropdown';

import styles from './toolbar.module.css';

type ToolbarProps = {
    templates: DropdownItem[];
    examples: DropdownItem[];
    onSelectTemplate: (slug: string) => void;
    onSelectExample: (slug: string) => void;
    /** Called when the user asks for a permalink to the project. */
    onShare: () => void;
    /** Called when the user asks to report a bug. */
    onReportBug: () => void;
};

/** GitHub's mark, the cat silhouette in a circle. */
const GitHubIcon = () => (
    <svg
        width="16"
        height="16"
        viewBox="0 0 16 16"
        fill="currentColor"
        aria-hidden="true"
    >
        <path d="M8 0c4.42 0 8 3.58 8 8a8.01 8.01 0 0 1-5.45 7.59c-.4.08-.55-.17-.55-.38 0-.27.01-1.13.01-2.2 0-.75-.25-1.23-.54-1.48 1.78-.2 3.65-.88 3.65-3.95 0-.88-.31-1.59-.82-2.15.08-.2.36-1.02-.08-2.12 0 0-.67-.22-2.2.82-.64-.18-1.32-.27-2-.27-.68 0-1.36.09-2 .27-1.53-1.03-2.2-.82-2.2-.82-.44 1.1-.16 1.92-.08 2.12-.51.56-.82 1.28-.82 2.15 0 3.06 1.86 3.75 3.64 3.95-.23.2-.44.55-.51 1.07-.46.21-1.61.55-2.33-.66-.15-.24-.6-.83-1.23-.82-.67.01-.27.38.01.53.34.19.73.9.82 1.13.16.45.68 1.31 2.69.94 0 .67.01 1.3.01 1.49 0 .21-.15.45-.55.38A7.995 7.995 0 0 1 0 8c0-4.42 3.58-8 8-8Z" />
    </svg>
);

const themeLabels: Record<ThemePreference, string> = {
    system: 'System',
    light: 'Light',
    dark: 'Dark',
};

const themeItems: DropdownItem[] = themePreferences.map((preference) => ({
    slug: preference,
    label: themeLabels[preference],
}));

type PendingAction = {
    kind: 'template' | 'example';
    slug: string;
    label: string;
};

export const Toolbar = ({
    templates,
    examples,
    onSelectTemplate,
    onSelectExample,
    onShare,
    onReportBug,
}: ToolbarProps) => {
    const themePreference = useThemeStore((state) => state.preference);
    const setThemePreference = useThemeStore((state) => state.setPreference);
    const handleSelectTheme = useCallback(
        (slug: string) => {
            const preference = themePreferences.find((p) => p === slug);
            if (preference) {
                setThemePreference(preference);
            }
        },
        [setThemePreference],
    );

    const [pendingAction, setPendingAction] = useState<PendingAction | null>(
        null,
    );

    const handleSelectTemplate = useCallback((slug: string, label: string) => {
        setPendingAction({ kind: 'template', slug, label });
    }, []);

    const handleSelectExample = useCallback((slug: string, label: string) => {
        setPendingAction({ kind: 'example', slug, label });
    }, []);

    const handleConfirm = useCallback(() => {
        if (!pendingAction) return;
        if (pendingAction.kind === 'template') {
            onSelectTemplate(pendingAction.slug);
        } else {
            onSelectExample(pendingAction.slug);
        }
        setPendingAction(null);
    }, [pendingAction, onSelectTemplate, onSelectExample]);

    const handleCancel = useCallback(() => {
        setPendingAction(null);
    }, []);

    return (
        <div className={styles.toolbar}>
            <Dropdown
                label="New Project"
                items={templates}
                onSelect={handleSelectTemplate}
            />
            <Dropdown
                label="Examples"
                items={examples}
                onSelect={handleSelectExample}
            />
            <button
                type="button"
                className={styles.dropdownButton}
                onClick={onShare}
            >
                Share
            </button>
            <button
                type="button"
                className={styles.dropdownButton}
                onClick={onReportBug}
            >
                Report a bug
            </button>
            <div className={styles.spacer}>
                <CheckStatusIndicator />
            </div>
            <a
                className={`${styles.dropdownButton} ${styles.link}`}
                href={REPO_URL}
                target="_blank"
                rel="noreferrer"
                title="Escalier on GitHub"
            >
                <GitHubIcon />
                GitHub
            </a>
            <Dropdown
                label={`Theme: ${themeLabels[themePreference]}`}
                items={themeItems}
                onSelect={handleSelectTheme}
                align="end"
                selectedSlug={themePreference}
            />

            {pendingAction && (
                <ConfirmDialog
                    title="Replace current project?"
                    message={`This will replace your current project with "${pendingAction.label}". Any unsaved changes will be lost.`}
                    confirmLabel="Replace"
                    destructive
                    onConfirm={handleConfirm}
                    onCancel={handleCancel}
                />
            )}
        </div>
    );
};
