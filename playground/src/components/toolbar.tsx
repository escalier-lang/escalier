import { useCallback, useState } from 'react';

import {
    type ThemePreference,
    themePreferences,
    useThemeStore,
} from '../theme';
import { ConfirmDialog } from './confirm-dialog';
import { Dropdown, type DropdownItem } from './dropdown';
import styles from './toolbar.module.css';

type ToolbarProps = {
    templates: DropdownItem[];
    examples: DropdownItem[];
    onSelectTemplate: (slug: string) => void;
    onSelectExample: (slug: string) => void;
};

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
            <div className={styles.spacer} />
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
