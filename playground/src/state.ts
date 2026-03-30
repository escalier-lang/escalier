import { type Dispatch, createContext, useContext } from 'react';

// ValidationResult type - will be fully implemented in Phase 2
export type ValidationResult =
    | { mode: 'single-package'; packageJson: object }
    | {
          mode: 'multi-package';
          packages: Array<{ name: string; path: string; packageJson: object }>;
      }
    | { mode: 'invalid'; errors: string[] };

export type Notification = {
    message: string;
    type: 'info' | 'warning' | 'error';
};

export type Tab = {
    path: string;
    scrollPos?: number;
};

export type Side = 'left' | 'right';

export type PlaygroundState = {
    leftTabs: Tab[];
    activeLeftTabIndex: number | null;
    rightTabs: Tab[];
    activeRightTabIndex: number | null;
    focusedSide: Side;
    initialCompileDone: boolean;
    validationResult: ValidationResult;
    notification: Notification | null;
};

export type PlaygroundAction =
    | { type: 'openFile'; path: string; side?: Side; scrollPos?: number }
    | { type: 'closeTab'; side: Side; index: number }
    | { type: 'setActiveTab'; side: Side; index: number }
    | { type: 'setFocusedSide'; side: Side }
    | { type: 'setInitialCompileDone' }
    | { type: 'moveTab'; from: Side; index: number }
    | { type: 'renameFile'; oldPath: string; newPath: string }
    | { type: 'deleteFile'; path: string }
    | { type: 'resetTabs'; primaryFile?: string }
    | { type: 'setValidationResult'; result: ValidationResult }
    | { type: 'showNotification'; notification: Notification }
    | { type: 'dismissNotification' };

export const initialState: PlaygroundState = {
    leftTabs: [{ path: '/bin/main.esc' }],
    activeLeftTabIndex: 0,
    rightTabs: [],
    activeRightTabIndex: null,
    focusedSide: 'left',
    initialCompileDone: false,
    validationResult: { mode: 'single-package', packageJson: {} },
    notification: null,
};

function getTabsForSide(state: PlaygroundState, side: Side): Tab[] {
    return side === 'left' ? state.leftTabs : state.rightTabs;
}

function setTabsForSide(
    state: PlaygroundState,
    side: Side,
    tabs: Tab[],
    activeIndex: number | null,
): PlaygroundState {
    return side === 'left'
        ? { ...state, leftTabs: tabs, activeLeftTabIndex: activeIndex }
        : { ...state, rightTabs: tabs, activeRightTabIndex: activeIndex };
}

export function playgroundReducer(
    state: PlaygroundState,
    action: PlaygroundAction,
): PlaygroundState {
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

        case 'setInitialCompileDone': {
            return { ...state, initialCompileDone: true };
        }

        case 'moveTab': {
            const { from, index } = action;
            const tabs = getTabsForSide(state, from);
            if (!Number.isInteger(index) || index < 0 || index >= tabs.length) {
                return state;
            }
            const tab = tabs[index];
            const to = from === 'left' ? 'right' : 'left';
            let result = playgroundReducer(state, {
                type: 'closeTab',
                side: from,
                index,
            });
            // When moving to left, set focus before openFile so it doesn't
            // route back to the right side
            if (to === 'left') {
                result = { ...result, focusedSide: 'left' };
            }
            return playgroundReducer(result, {
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
                result = playgroundReducer(result, {
                    type: 'closeTab',
                    side: 'left',
                    index: leftIndex,
                });
            }
            const rightIndex = result.rightTabs.findIndex(
                (tab) => tab.path === action.path,
            );
            if (rightIndex !== -1) {
                result = playgroundReducer(result, {
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
                initialCompileDone: false,
            };
        }

        case 'setValidationResult': {
            return { ...state, validationResult: action.result };
        }

        case 'showNotification': {
            return { ...state, notification: action.notification };
        }

        case 'dismissNotification': {
            return { ...state, notification: null };
        }
    }
}

export const PlaygroundStateContext =
    createContext<PlaygroundState>(initialState);
export const PlaygroundDispatchContext = createContext<
    Dispatch<PlaygroundAction>
>(() => {});

export function usePlaygroundState(): PlaygroundState {
    return useContext(PlaygroundStateContext);
}

export function usePlaygroundDispatch(): Dispatch<PlaygroundAction> {
    return useContext(PlaygroundDispatchContext);
}
