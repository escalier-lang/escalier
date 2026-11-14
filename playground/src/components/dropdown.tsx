import { useCallback, useEffect, useRef, useState } from 'react';

import styles from './toolbar.module.css';

export type DropdownItem = {
    slug: string;
    label: string;
};

type DropdownProps = {
    label: string;
    items: DropdownItem[];
    onSelect: (slug: string, label: string) => void;
    /** Which edge of the button the menu lines up with. Defaults to `start`. */
    align?: 'start' | 'end';
    /**
     * The slug of the item that's currently chosen. When set, the items are radio
     * items and the chosen one is checked.
     */
    selectedSlug?: string;
};

export const Dropdown = ({
    label,
    items,
    onSelect,
    align = 'start',
    selectedSlug,
}: DropdownProps) => {
    const [open, setOpen] = useState(false);
    const containerRef = useRef<HTMLDivElement>(null);

    const handleBlur = useCallback((e: React.FocusEvent<HTMLDivElement>) => {
        if (
            containerRef.current &&
            !containerRef.current.contains(e.relatedTarget as Node)
        ) {
            setOpen(false);
        }
    }, []);

    useEffect(() => {
        if (!open) return;
        const handleKeyDown = (e: KeyboardEvent) => {
            if (e.key === 'Escape') {
                setOpen(false);
            }
        };
        document.addEventListener('keydown', handleKeyDown);
        return () => document.removeEventListener('keydown', handleKeyDown);
    }, [open]);

    return (
        <div
            className={styles.dropdownContainer}
            ref={containerRef}
            onBlur={handleBlur}
        >
            <button
                type="button"
                className={styles.dropdownButton}
                onClick={() => setOpen((prev) => !prev)}
                aria-expanded={open}
                aria-haspopup="true"
            >
                {label}
                <span className={styles.caret}>&#9662;</span>
            </button>
            {open && (
                <div
                    className={
                        align === 'end'
                            ? `${styles.dropdown} ${styles.dropdownEnd}`
                            : styles.dropdown
                    }
                    role="menu"
                >
                    {items.map((item) => (
                        <button
                            key={item.slug}
                            type="button"
                            className={styles.dropdownItem}
                            role={
                                selectedSlug === undefined
                                    ? 'menuitem'
                                    : 'menuitemradio'
                            }
                            aria-checked={
                                selectedSlug === undefined
                                    ? undefined
                                    : item.slug === selectedSlug
                            }
                            onClick={() => {
                                setOpen(false);
                                onSelect(item.slug, item.label);
                            }}
                        >
                            {item.label}
                        </button>
                    ))}
                </div>
            )}
        </div>
    );
};
