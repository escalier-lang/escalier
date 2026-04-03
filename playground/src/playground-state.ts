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
    | { type: 'resetCompile' }
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

        case 'resetCompile': {
            return { ...state, initialCompileDone: false };
        }

        case 'setValidationResult': {
            return { ...state, validationResult: action.result };
        }

        default: {
            const _exhaustive: never = action;
            void _exhaustive;
            return state;
        }
    }
}
