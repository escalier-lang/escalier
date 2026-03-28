import { createContext, useContext, type Dispatch } from 'react';

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

export type OutputTab = 'js' | 'map' | 'dts';

export type Tab = {
    path: string;
    scrollPos?: number;
};

export type PlaygroundState = {
    openTabs: Tab[];
    activeTabIndex: number | null;
    activeOutputTab: OutputTab;
    validationResult: ValidationResult;
    notification: Notification | null;
};

export type PlaygroundAction =
    | { type: 'openFile'; path: string }
    | { type: 'closeTab'; index: number }
    | { type: 'setActiveTab'; index: number }
    | { type: 'setActiveOutputTab'; tab: OutputTab }
    | { type: 'renameFile'; oldPath: string; newPath: string }
    | { type: 'deleteFile'; path: string }
    | { type: 'resetTabs'; primaryFile?: string }
    | { type: 'setValidationResult'; result: ValidationResult }
    | { type: 'showNotification'; notification: Notification }
    | { type: 'dismissNotification' };

export const initialState: PlaygroundState = {
    openTabs: [{ path: '/bin/main.esc' }],
    activeTabIndex: 0,
    activeOutputTab: 'js',
    validationResult: { mode: 'single-package', packageJson: {} },
    notification: null,
};

export function playgroundReducer(
    state: PlaygroundState,
    action: PlaygroundAction,
): PlaygroundState {
    switch (action.type) {
        case 'openFile': {
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
            const newTabs = state.openTabs.filter(
                (_, i) => i !== action.index,
            );
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

        case 'setActiveOutputTab': {
            return { ...state, activeOutputTab: action.tab };
        }

        case 'renameFile': {
            const newTabs = state.openTabs.map((tab) =>
                tab.path === action.oldPath
                    ? { ...tab, path: action.newPath }
                    : tab,
            );
            return { ...state, openTabs: newTabs };
        }

        case 'deleteFile': {
            const deleteIndex = state.openTabs.findIndex(
                (tab) => tab.path === action.path,
            );
            if (deleteIndex === -1) {
                return state;
            }
            // Reuse closeTab logic
            return playgroundReducer(state, {
                type: 'closeTab',
                index: deleteIndex,
            });
        }

        case 'resetTabs': {
            const primaryFile = action.primaryFile ?? '/lib/index.esc';
            return {
                ...state,
                openTabs: [{ path: primaryFile }],
                activeTabIndex: 0,
                activeOutputTab: 'js',
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

export const PlaygroundStateContext = createContext<PlaygroundState>(initialState);
export const PlaygroundDispatchContext = createContext<Dispatch<PlaygroundAction>>(
    () => {},
);

export function usePlaygroundState(): PlaygroundState {
    return useContext(PlaygroundStateContext);
}

export function usePlaygroundDispatch(): Dispatch<PlaygroundAction> {
    return useContext(PlaygroundDispatchContext);
}
