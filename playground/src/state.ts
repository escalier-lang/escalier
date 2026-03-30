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
    openTabs: Tab[];
    activeTabIndex: number | null;
    rightTabs: Tab[];
    activeRightTabIndex: number | null;
    focusedSide: Side;
    initialCompileDone: boolean;
    validationResult: ValidationResult;
    notification: Notification | null;
};

export type PlaygroundAction =
    | { type: 'openFile'; path: string }
    | { type: 'closeTab'; index: number }
    | { type: 'setActiveTab'; index: number }
    | { type: 'openRightFile'; path: string }
    | { type: 'closeRightTab'; index: number }
    | { type: 'setActiveRightTab'; index: number }
    | { type: 'setFocusedSide'; side: Side }
    | { type: 'setInitialCompileDone' }
    | { type: 'moveTabToRight'; index: number }
    | { type: 'moveTabToLeft'; index: number }
    | { type: 'renameFile'; oldPath: string; newPath: string }
    | { type: 'deleteFile'; path: string }
    | { type: 'resetTabs'; primaryFile?: string }
    | { type: 'setValidationResult'; result: ValidationResult }
    | { type: 'showNotification'; notification: Notification }
    | { type: 'dismissNotification' };

export const initialState: PlaygroundState = {
    openTabs: [{ path: '/bin/main.esc' }],
    activeTabIndex: 0,
    rightTabs: [],
    activeRightTabIndex: null,
    focusedSide: 'left',
    initialCompileDone: false,
    validationResult: { mode: 'single-package', packageJson: {} },
    notification: null,
};

export function playgroundReducer(
    state: PlaygroundState,
    action: PlaygroundAction,
): PlaygroundState {
    switch (action.type) {
        case 'openFile': {
            if (state.focusedSide === 'right') {
                return playgroundReducer(state, {
                    type: 'openRightFile',
                    path: action.path,
                });
            }
            const existingIndex = state.openTabs.findIndex(
                (tab) => tab.path === action.path,
            );
            if (existingIndex !== -1) {
                // Tab already open, just activate it
                return { ...state, activeTabIndex: existingIndex };
            }
            const newTabs = [...state.openTabs, { path: action.path }];
            return {
                ...state,
                openTabs: newTabs,
                activeTabIndex: newTabs.length - 1,
            };
        }

        case 'closeTab': {
            if (
                !Number.isInteger(action.index) ||
                action.index < 0 ||
                action.index >= state.openTabs.length
            ) {
                return state;
            }
            const newTabs = state.openTabs.filter((_, i) => i !== action.index);
            let newActiveIndex = state.activeTabIndex;

            if (newTabs.length === 0) {
                newActiveIndex = null;
            } else if (state.activeTabIndex === action.index) {
                // Closing the active tab: activate the next tab, or previous if last
                newActiveIndex =
                    action.index >= newTabs.length
                        ? newTabs.length - 1
                        : action.index;
            } else if (
                state.activeTabIndex !== null &&
                state.activeTabIndex > action.index
            ) {
                // Active tab shifted left
                newActiveIndex = state.activeTabIndex - 1;
            }

            return {
                ...state,
                openTabs: newTabs,
                activeTabIndex: newActiveIndex,
            };
        }

        case 'setActiveTab': {
            if (action.index < 0 || action.index >= state.openTabs.length) {
                return state;
            }
            return { ...state, activeTabIndex: action.index };
        }

        case 'openRightFile': {
            const existingIndex = state.rightTabs.findIndex(
                (tab) => tab.path === action.path,
            );
            if (existingIndex !== -1) {
                return { ...state, activeRightTabIndex: existingIndex };
            }
            const newTabs = [...state.rightTabs, { path: action.path }];
            return {
                ...state,
                rightTabs: newTabs,
                activeRightTabIndex: newTabs.length - 1,
            };
        }

        case 'closeRightTab': {
            if (
                !Number.isInteger(action.index) ||
                action.index < 0 ||
                action.index >= state.rightTabs.length
            ) {
                return state;
            }
            const newTabs = state.rightTabs.filter(
                (_, i) => i !== action.index,
            );
            let newActiveIndex = state.activeRightTabIndex;

            if (newTabs.length === 0) {
                newActiveIndex = null;
            } else if (state.activeRightTabIndex === action.index) {
                newActiveIndex =
                    action.index >= newTabs.length
                        ? newTabs.length - 1
                        : action.index;
            } else if (
                state.activeRightTabIndex !== null &&
                state.activeRightTabIndex > action.index
            ) {
                newActiveIndex = state.activeRightTabIndex - 1;
            }

            return {
                ...state,
                rightTabs: newTabs,
                activeRightTabIndex: newActiveIndex,
            };
        }

        case 'setActiveRightTab': {
            if (
                action.index < 0 ||
                action.index >= state.rightTabs.length
            ) {
                return state;
            }
            return { ...state, activeRightTabIndex: action.index };
        }

        case 'setFocusedSide': {
            return { ...state, focusedSide: action.side };
        }

        case 'setInitialCompileDone': {
            return { ...state, initialCompileDone: true };
        }

        case 'moveTabToRight': {
            if (
                action.index < 0 ||
                action.index >= state.openTabs.length
            ) {
                return state;
            }
            const tab = state.openTabs[action.index];
            // Close from left, open on right
            const closed = playgroundReducer(state, {
                type: 'closeTab',
                index: action.index,
            });
            return playgroundReducer(closed, {
                type: 'openRightFile',
                path: tab.path,
            });
        }

        case 'moveTabToLeft': {
            if (
                action.index < 0 ||
                action.index >= state.rightTabs.length
            ) {
                return state;
            }
            const tab = state.rightTabs[action.index];
            // Close from right, open on left
            const closed = playgroundReducer(state, {
                type: 'closeRightTab',
                index: action.index,
            });
            return playgroundReducer(closed, {
                type: 'openFile',
                path: tab.path,
            });
        }

        case 'renameFile': {
            const newTabs = state.openTabs.map((tab) =>
                tab.path === action.oldPath
                    ? { ...tab, path: action.newPath }
                    : tab,
            );
            const newRightTabs = state.rightTabs.map((tab) =>
                tab.path === action.oldPath
                    ? { ...tab, path: action.newPath }
                    : tab,
            );
            return { ...state, openTabs: newTabs, rightTabs: newRightTabs };
        }

        case 'deleteFile': {
            let result = state;
            const leftIndex = state.openTabs.findIndex(
                (tab) => tab.path === action.path,
            );
            if (leftIndex !== -1) {
                result = playgroundReducer(result, {
                    type: 'closeTab',
                    index: leftIndex,
                });
            }
            const rightIndex = state.rightTabs.findIndex(
                (tab) => tab.path === action.path,
            );
            if (rightIndex !== -1) {
                result = playgroundReducer(result, {
                    type: 'closeRightTab',
                    index: rightIndex,
                });
            }
            return result;
        }

        case 'resetTabs': {
            const primaryFile = action.primaryFile ?? '/lib/index.esc';
            return {
                ...state,
                openTabs: [{ path: primaryFile }],
                activeTabIndex: 0,
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
