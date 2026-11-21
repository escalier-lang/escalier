import type { Side } from './editor-state';
import { displayName } from './paths';
import { TabContextMenu } from './tab-context-menu';
import { TAB_DRAG_TYPE } from './tab-drag';

import styles from './editor.module.css';

type TabItemProps = {
    path: string;
    isActive: boolean;
    isFocused: boolean;
    panelId: string;
    side: Side;
    /** Where the tab's context menu shows, or null when it is closed. */
    contextMenuAt: { x: number; y: number } | null;
    onActivate: () => void;
    onClose: () => void;
    onMove: () => void;
    onContextMenu: (x: number, y: number) => void;
    onDragStart: () => void;
    onDragEnd: () => void;
};

/** A tab for the file at path, with a close button and a context menu. */
export const TabItem = ({
    path,
    isActive,
    isFocused,
    panelId,
    side,
    contextMenuAt,
    onActivate,
    onClose,
    onMove,
    onContextMenu,
    onDragStart,
    onDragEnd,
}: TabItemProps) => {
    const name = displayName(path);
    const className = `${styles.tab} ${isActive && isFocused ? styles.activeTab : styles.visibleTab}`;

    return (
        <div
            role="tab"
            tabIndex={0}
            aria-selected={isActive}
            aria-controls={panelId}
            className={className}
            onClick={onActivate}
            onKeyDown={(e) => {
                if (e.key === 'Enter' || e.key === ' ') onActivate();
            }}
            onContextMenu={(e) => {
                e.preventDefault();
                onContextMenu(e.clientX, e.clientY);
            }}
            draggable
            onDragStart={(e) => {
                // Firefox starts a drag only when the drag carries data.
                e.dataTransfer.setData(TAB_DRAG_TYPE, path);
                e.dataTransfer.effectAllowed = 'move';
                onDragStart();
            }}
            onDragEnd={onDragEnd}
        >
            <span className={styles.tabLabel}>{name}</span>
            <button
                type="button"
                className={styles.closeButton}
                onClick={(e) => {
                    e.stopPropagation();
                    onClose();
                }}
                aria-label={`Close ${name}`}
                tabIndex={0}
            >
                &times;
            </button>
            {contextMenuAt && (
                <TabContextMenu
                    side={side}
                    x={contextMenuAt.x}
                    y={contextMenuAt.y}
                    onMove={onMove}
                    onClose={onClose}
                />
            )}
        </div>
    );
};
