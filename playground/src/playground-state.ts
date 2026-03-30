import { type Dispatch, createContext, useContext } from 'react';

export type ValidationResult =
    | { mode: 'single-package'; packageJson: object }
    | {
          mode: 'multi-package';
          packages: Array<{ name: string; path: string; packageJson: object }>;
      }
    | { mode: 'invalid'; errors: string[] };

export type PlaygroundState = {
    initialCompileDone: boolean;
    validationResult: ValidationResult;
};

export type PlaygroundAction =
    | { type: 'setInitialCompileDone' }
    | { type: 'setValidationResult'; result: ValidationResult };

export const initialPlaygroundState: PlaygroundState = {
    initialCompileDone: false,
    validationResult: { mode: 'single-package', packageJson: {} },
};

export function playgroundReducer(
    state: PlaygroundState,
    action: PlaygroundAction,
): PlaygroundState {
    switch (action.type) {
        case 'setInitialCompileDone': {
            return { ...state, initialCompileDone: true };
        }

        case 'setValidationResult': {
            return { ...state, validationResult: action.result };
        }
    }
}

export const PlaygroundStateContext = createContext<PlaygroundState>(
    initialPlaygroundState,
);
export const PlaygroundDispatchContext = createContext<
    Dispatch<PlaygroundAction>
>(() => {});

export function usePlaygroundState(): PlaygroundState {
    return useContext(PlaygroundStateContext);
}

export function usePlaygroundDispatch(): Dispatch<PlaygroundAction> {
    return useContext(PlaygroundDispatchContext);
}
