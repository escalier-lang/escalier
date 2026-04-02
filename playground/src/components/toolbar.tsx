import { useCallback, useRef, useState } from 'react';

import { ConfirmDialog } from './confirm-dialog';
import styles from './toolbar.module.css';

type DropdownItem = {
    slug: string;
    label: string;
};

type ToolbarProps = {
    templates: DropdownItem[];
    examples: DropdownItem[];
    onSelectTemplate: (slug: string) => void;
    onSelectExample: (slug: string) => void;
};

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
    const [openDropdown, setOpenDropdown] = useState<
        'templates' | 'examples' | null
    >(null);
    const [pendingAction, setPendingAction] = useState<PendingAction | null>(
        null,
    );
    const templatesRef = useRef<HTMLDivElement>(null);
    const examplesRef = useRef<HTMLDivElement>(null);

    const handleBlur = useCallback(
        (dropdown: 'templates' | 'examples') =>
            (e: React.FocusEvent<HTMLDivElement>) => {
                const ref =
                    dropdown === 'templates' ? templatesRef : examplesRef;
                if (
                    ref.current &&
                    !ref.current.contains(e.relatedTarget as Node)
                ) {
                    setOpenDropdown(null);
                }
            },
        [],
    );

    const handleSelect = useCallback(
        (kind: 'template' | 'example', slug: string, label: string) => {
            setOpenDropdown(null);
            setPendingAction({ kind, slug, label });
        },
        [],
    );

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
            <div
                className={styles.dropdownContainer}
                ref={templatesRef}
                onBlur={handleBlur('templates')}
            >
                <button
                    type="button"
                    className={styles.dropdownButton}
                    onClick={() =>
                        setOpenDropdown(
                            openDropdown === 'templates' ? null : 'templates',
                        )
                    }
                    aria-expanded={openDropdown === 'templates'}
                    aria-haspopup="true"
                >
                    New Project
                    <span className={styles.caret}>&#9662;</span>
                </button>
                {openDropdown === 'templates' && (
                    <div className={styles.dropdown} role="menu">
                        {templates.map((t) => (
                            <button
                                key={t.slug}
                                type="button"
                                className={styles.dropdownItem}
                                role="menuitem"
                                onClick={() =>
                                    handleSelect('template', t.slug, t.label)
                                }
                            >
                                {t.label}
                            </button>
                        ))}
                    </div>
                )}
            </div>

            <div
                className={styles.dropdownContainer}
                ref={examplesRef}
                onBlur={handleBlur('examples')}
            >
                <button
                    type="button"
                    className={styles.dropdownButton}
                    onClick={() =>
                        setOpenDropdown(
                            openDropdown === 'examples' ? null : 'examples',
                        )
                    }
                    aria-expanded={openDropdown === 'examples'}
                    aria-haspopup="true"
                >
                    Examples
                    <span className={styles.caret}>&#9662;</span>
                </button>
                {openDropdown === 'examples' && (
                    <div className={styles.dropdown} role="menu">
                        {examples.map((e) => (
                            <button
                                key={e.slug}
                                type="button"
                                className={styles.dropdownItem}
                                role="menuitem"
                                onClick={() =>
                                    handleSelect('example', e.slug, e.label)
                                }
                            >
                                {e.label}
                            </button>
                        ))}
                    </div>
                )}
            </div>

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
