import { useEffect, useState } from 'react';

import type { Side, Tab } from './editor-state';
import { useEditorStore } from './editor-store';
import type { DropHandlers } from './tab-drag';
import { TabItem } from './tab-item';

import styles from './editor.module.css';

/** The tab whose context menu is open, and where the menu shows. */
type ContextMenuState = { tabPath: string; x: number; y: number };

type TabListProps = {
    side: Side;
    tabs: readonly Tab[];
    activeIndex: number | null;
    /** Whether files opened from the explorer open on this side. */
    isFocused: boolean;
    isDropTarget: boolean;
    dropHandlers: DropHandlers;
    onDragStart: (index: number) => void;
    onDragEnd: () => void;
};

/** The row of tabs above one side's editor. */
export const TabList = ({
    side,
    tabs,
    activeIndex,
    isFocused,
    isDropTarget,
    dropHandlers,
    onDragStart,
    onDragEnd,
}: TabListProps) => {
    const dispatch = useEditorStore((state) => state.dispatch);
    const [contextMenu, setContextMenu] = useState<ContextMenuState | null>(
        null,
    );

    // Dismiss context menu on click or pointer-down elsewhere.
    // pointerdown covers both left- and right-clicks and fires before
    // contextmenu, so it dismisses the menu before a new one opens.
    // We intentionally omit 'contextmenu' because it would bubble from
    // the tab and immediately clear the menu that was just opened.
    useEffect(() => {
        if (!contextMenu) return;
        const dismiss = () => setContextMenu(null);
        window.addEventListener('click', dismiss);
        window.addEventListener('pointerdown', dismiss);
        return () => {
            window.removeEventListener('click', dismiss);
            window.removeEventListener('pointerdown', dismiss);
        };
    }, [contextMenu]);

    const listClass = side === 'left' ? styles.inputTabs : styles.outputTabs;
    return (
        <div
            {...dropHandlers}
            className={`${listClass} ${isDropTarget ? styles.dropTarget : ''}`}
            role="tablist"
            data-testid={side === 'left' ? 'input-tablist' : 'output-tablist'}
        >
            {tabs.map((tab, i) => (
                <TabItem
                    key={tab.path}
                    path={tab.path}
                    isActive={i === activeIndex}
                    isFocused={isFocused}
                    panelId={side === 'left' ? 'input-panel' : 'output-panel'}
                    side={side}
                    contextMenuAt={
                        contextMenu?.tabPath === tab.path ? contextMenu : null
                    }
                    onActivate={() => {
                        dispatch({ type: 'setFocusedSide', side });
                        dispatch({ type: 'setActiveTab', side, index: i });
                    }}
                    onClose={() => {
                        setContextMenu(null);
                        dispatch({ type: 'closeTab', side, index: i });
                    }}
                    onMove={() => {
                        setContextMenu(null);
                        dispatch({ type: 'moveTab', from: side, index: i });
                    }}
                    onContextMenu={(x, y) =>
                        setContextMenu({ tabPath: tab.path, x, y })
                    }
                    onDragStart={() => {
                        setContextMenu(null);
                        onDragStart(i);
                    }}
                    onDragEnd={onDragEnd}
                />
            ))}
        </div>
    );
};
