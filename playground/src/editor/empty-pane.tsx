import type { Side } from './editor-state';
import type { DropHandlers } from './tab-drag';

import styles from './editor.module.css';

type EmptyPaneProps = {
    side: Side;
    /** Whether files opened from the explorer open on this side. */
    isFocused: boolean;
    isDropTarget: boolean;
    dropHandlers: DropHandlers;
    onFocus: () => void;
};

/**
 * Fills a side that has no tabs. Clicking it makes the side the one files open on,
 * and a tab dragged from the other side can be dropped on it.
 */
export const EmptyPane = ({
    side,
    isFocused,
    isDropTarget,
    dropHandlers,
    onFocus,
}: EmptyPaneProps) => (
    <button
        type="button"
        className={[
            styles.emptyState,
            side === 'left' ? styles.inputEmpty : styles.outputEmpty,
            isFocused ? styles.focusedEmpty : '',
            isDropTarget ? styles.dropTarget : '',
        ]
            .filter(Boolean)
            .join(' ')}
        onClick={onFocus}
        {...dropHandlers}
    >
        {isFocused
            ? 'Open a file from the explorer or drag a tab here'
            : 'Click to open files here, or drag a tab here'}
    </button>
);
