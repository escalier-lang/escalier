import type { Side } from './editor-state';

import styles from './editor.module.css';

type TabContextMenuProps = {
    /** The side of the tab the menu belongs to. */
    side: Side;
    /** The menu's position in viewport coordinates. */
    x: number;
    y: number;
    onMove: () => void;
    onClose: () => void;
};

/** The menu a right-click on a tab opens, with items to move the tab and to close it. */
export const TabContextMenu = ({
    side,
    x,
    y,
    onMove,
    onClose,
}: TabContextMenuProps) => (
    <div
        className={styles.contextMenu}
        style={{ left: x, top: y }}
        onClick={(e) => e.stopPropagation()}
        onPointerDown={(e) => e.stopPropagation()}
        onKeyDown={(e) => e.stopPropagation()}
    >
        <button
            type="button"
            className={styles.contextMenuItem}
            onClick={() => onMove()}
        >
            {side === 'left' ? 'Move to Right' : 'Move to Left'}
        </button>
        <button
            type="button"
            className={styles.contextMenuItem}
            onClick={() => onClose()}
        >
            Close
        </button>
    </div>
);
