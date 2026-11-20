/** What the playground shows about a language server crash, for a bug report. */
export type CrashReport = {
    /** The first line of the server's account of the crash, for an issue title. */
    headline: string;
    /** The report without its playground link. crashReportText adds the link. */
    text: string;
    /** A permalink to the project as it was when the server crashed. */
    permalink?: string;
};

export type Notification = {
    message: string;
    type: 'info' | 'warning' | 'error';
    /** Set on a toast about a language server crash. The toast offers its details. */
    crashReport?: CrashReport;
};

export type Tab = {
    path: string;
    scrollPos?: number;
};

export type Side = 'left' | 'right';

/** The paths of the tabs open on one side, in order, and the index of the visible one. */
export type TabLayout = {
    paths: string[];
    /** The index into paths of the visible tab, or null when paths is empty. */
    active: number | null;
};

export type EditorState = {
    leftTabs: Tab[];
    activeLeftTabIndex: number | null;
    rightTabs: Tab[];
    activeRightTabIndex: number | null;
    focusedSide: Side;
    notification: Notification | null;
    /**
     * The crash report of the latest crash or panic toast since the project loaded, kept
     * after the toast is dismissed, or null when none has shown.
     */
    lastCrashReport: CrashReport | null;
    /**
     * Incremented by resetTabs when the user switches projects. This causes
     * the editor to dispose all existing Monaco models so they get recreated
     * with fresh content from BrowserFS, rather than showing stale content
     * from the previous project's files at the same paths.
     */
    refreshKey: number;
};

export type EditorAction =
    | { type: 'openFile'; path: string; side?: Side; scrollPos?: number }
    | { type: 'closeTab'; side: Side; index: number }
    | { type: 'setActiveTab'; side: Side; index: number }
    | { type: 'setFocusedSide'; side: Side }
    | { type: 'moveTab'; from: Side; index: number }
    | { type: 'renameFile'; oldPath: string; newPath: string }
    | { type: 'deleteFile'; path: string }
    | { type: 'resetTabs'; primaryFile?: string }
    | { type: 'restoreTabs'; left: TabLayout; right: TabLayout }
    | { type: 'showNotification'; notification: Notification }
    | { type: 'dismissNotification' };

export const initialEditorState: EditorState = {
    leftTabs: [{ path: '/bin/main.esc' }],
    activeLeftTabIndex: 0,
    rightTabs: [],
    activeRightTabIndex: null,
    focusedSide: 'left',
    notification: null,
    lastCrashReport: null,
    refreshKey: 0,
};

function getTabsForSide(state: EditorState, side: Side): Tab[] {
    return side === 'left' ? state.leftTabs : state.rightTabs;
}

function setTabsForSide(
    state: EditorState,
    side: Side,
    tabs: Tab[],
    activeIndex: number | null,
): EditorState {
    return side === 'left'
        ? { ...state, leftTabs: tabs, activeLeftTabIndex: activeIndex }
        : { ...state, rightTabs: tabs, activeRightTabIndex: activeIndex };
}

export function editorReducer(
    state: EditorState,
    action: EditorAction,
): EditorState {
    switch (action.type) {
        case 'openFile': {
            const side = action.side ?? state.focusedSide;
            const tabs = getTabsForSide(state, side);
            const existingIndex = tabs.findIndex(
                (tab) => tab.path === action.path,
            );
            if (existingIndex !== -1) {
                return setTabsForSide(state, side, tabs, existingIndex);
            }
            const newTab: Tab = { path: action.path };
            if (action.scrollPos !== undefined) {
                newTab.scrollPos = action.scrollPos;
            }
            const newTabs = [...tabs, newTab];
            return setTabsForSide(state, side, newTabs, newTabs.length - 1);
        }

        case 'closeTab': {
            const { side, index } = action;
            const tabs = getTabsForSide(state, side);
            const activeIndex =
                side === 'left'
                    ? state.activeLeftTabIndex
                    : state.activeRightTabIndex;

            if (!Number.isInteger(index) || index < 0 || index >= tabs.length) {
                return state;
            }
            const newTabs = tabs.filter((_, i) => i !== index);
            let newActiveIndex = activeIndex;

            if (newTabs.length === 0) {
                newActiveIndex = null;
            } else if (activeIndex === index) {
                newActiveIndex =
                    index >= newTabs.length ? newTabs.length - 1 : index;
            } else if (activeIndex !== null && activeIndex > index) {
                newActiveIndex = activeIndex - 1;
            }

            return setTabsForSide(state, side, newTabs, newActiveIndex);
        }

        case 'setActiveTab': {
            const { side, index } = action;
            const tabs = getTabsForSide(state, side);
            if (!Number.isInteger(index) || index < 0 || index >= tabs.length) {
                return state;
            }
            return setTabsForSide(state, side, tabs, index);
        }

        case 'setFocusedSide': {
            return { ...state, focusedSide: action.side };
        }

        case 'moveTab': {
            const { from, index } = action;
            const tabs = getTabsForSide(state, from);
            if (!Number.isInteger(index) || index < 0 || index >= tabs.length) {
                return state;
            }
            const tab = tabs[index];
            const to = from === 'left' ? 'right' : 'left';
            let result = editorReducer(state, {
                type: 'closeTab',
                side: from,
                index,
            });
            // set focus before openFile so it doesn't route back to the opposite
            // site
            result = { ...result, focusedSide: to };
            return editorReducer(result, {
                type: 'openFile',
                path: tab.path,
                side: to,
                scrollPos: tab.scrollPos,
            });
        }

        case 'renameFile': {
            const newTabs = state.leftTabs.map((tab) =>
                tab.path === action.oldPath
                    ? { ...tab, path: action.newPath }
                    : tab,
            );
            const newRightTabs = state.rightTabs.map((tab) =>
                tab.path === action.oldPath
                    ? { ...tab, path: action.newPath }
                    : tab,
            );
            return { ...state, leftTabs: newTabs, rightTabs: newRightTabs };
        }

        case 'deleteFile': {
            let result = state;
            const leftIndex = result.leftTabs.findIndex(
                (tab) => tab.path === action.path,
            );
            if (leftIndex !== -1) {
                result = editorReducer(result, {
                    type: 'closeTab',
                    side: 'left',
                    index: leftIndex,
                });
            }
            const rightIndex = result.rightTabs.findIndex(
                (tab) => tab.path === action.path,
            );
            if (rightIndex !== -1) {
                result = editorReducer(result, {
                    type: 'closeTab',
                    side: 'right',
                    index: rightIndex,
                });
            }
            return result;
        }

        case 'resetTabs': {
            const primaryFile = action.primaryFile ?? '/lib/index.esc';
            return {
                ...state,
                leftTabs: [{ path: primaryFile }],
                activeLeftTabIndex: 0,
                rightTabs: [],
                activeRightTabIndex: null,
                focusedSide: 'left',
                // A crash in the previous project says nothing about this one.
                lastCrashReport: null,
                refreshKey: state.refreshKey + 1,
            };
        }

        case 'restoreTabs': {
            const toTabs = (layout: TabLayout) =>
                layout.paths.map((path) => ({ path }));
            return {
                ...state,
                leftTabs: toTabs(action.left),
                activeLeftTabIndex: action.left.active,
                rightTabs: toTabs(action.right),
                activeRightTabIndex: action.right.active,
                focusedSide: 'left',
                refreshKey: state.refreshKey + 1,
            };
        }

        case 'showNotification': {
            return {
                ...state,
                notification: action.notification,
                lastCrashReport:
                    action.notification.crashReport ?? state.lastCrashReport,
            };
        }

        case 'dismissNotification': {
            return { ...state, notification: null };
        }

        default: {
            const _exhaustive: never = action;
            void _exhaustive;
            return state;
        }
    }
}
