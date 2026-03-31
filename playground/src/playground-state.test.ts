import { describe, expect, test } from 'vitest';

import { initialPlaygroundState, playgroundReducer } from './playground-state';

describe('playgroundReducer', () => {
    describe('setInitialCompileDone', () => {
        test('sets initialCompileDone to true', () => {
            const next = playgroundReducer(initialPlaygroundState, {
                type: 'setInitialCompileDone',
            });
            expect(next.initialCompileDone).toBe(true);
        });
    });

    describe('setValidationResult', () => {
        test('sets the validation result', () => {
            const result = { mode: 'invalid' as const, errors: ['bad'] };
            const next = playgroundReducer(initialPlaygroundState, {
                type: 'setValidationResult',
                result,
            });
            expect(next.validationResult).toBe(result);
        });
    });
});
