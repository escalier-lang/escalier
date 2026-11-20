import { type DragEvent, useState } from 'react';

import type { Side } from './editor-state';
import { useEditorStore } from './editor-store';

/**
 * The drag data type a tab sets. Monaco ignores it, so a tab dropped on an editor
 * inserts no text.
 */
export const TAB_DRAG_TYPE = 'application/x-escalier-tab';

/** The handlers an element spreads onto itself to take a dropped tab. */
export type DropHandlers = {
    onDragOver: (e: DragEvent) => void;
    onDragLeave: (e: DragEvent) => void;
    onDrop: (e: DragEvent) => void;
};

/** The tab being dragged. */
type DraggedTab = { side: Side; index: number };

export type TabDrag = {
    /** The side whose drop target a tab is over, or null when there is none. */
    dropSide: Side | null;
    /**
     * Returns the drag handlers that let an element on side take a tab dragged from
     * the other side. Dropping the tab moves it, as "Move to Right" and "Move to Left" do.
     */
    dropHandlers: (side: Side) => DropHandlers;
    startDrag: (side: Side, index: number) => void;
    endDrag: () => void;
};

/** Returns the state of a tab drag between the two sides and the functions that update it. */
export function useTabDrag(): TabDrag {
    const dispatch = useEditorStore((state) => state.dispatch);
    const [draggedTab, setDraggedTab] = useState<DraggedTab | null>(null);
    const [dropSide, setDropSide] = useState<Side | null>(null);

    const endDrag = () => {
        setDraggedTab(null);
        setDropSide(null);
    };

    const dropHandlers = (side: Side): DropHandlers => {
        const accepts = draggedTab !== null && draggedTab.side !== side;
        return {
            onDragOver: (e) => {
                if (!accepts) return;
                // Cancelling dragover marks the element as a drop target.
                e.preventDefault();
                e.dataTransfer.dropEffect = 'move';
                setDropSide(side);
            },
            onDragLeave: (e) => {
                // dragleave also fires on moving between the element's children.
                if (e.currentTarget.contains(e.relatedTarget as Node | null)) {
                    return;
                }
                setDropSide(null);
            },
            onDrop: (e) => {
                if (!accepts) return;
                e.preventDefault();
                endDrag();
                dispatch({
                    type: 'moveTab',
                    from: draggedTab.side,
                    index: draggedTab.index,
                });
            },
        };
    };

    return {
        dropSide,
        dropHandlers,
        startDrag: (side, index) => setDraggedTab({ side, index }),
        endDrag,
    };
}
