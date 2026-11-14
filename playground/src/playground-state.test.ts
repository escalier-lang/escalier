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

    describe('markProjectModified', () => {
        test('marks the project modified', () => {
            const state = playgroundReducer(initialPlaygroundState, {
                type: 'markProjectModified',
            });
            expect(state.projectModified).toBe(true);
        });

        test('returns the same state when the project is already modified', () => {
            const modified = {
                ...initialPlaygroundState,
                projectModified: true,
            };
            expect(
                playgroundReducer(modified, { type: 'markProjectModified' }),
            ).toBe(modified);
        });
    });

    describe('projectLoaded', () => {
        test('clears projectModified', () => {
            const modified = {
                ...initialPlaygroundState,
                projectModified: true,
            };
            const state = playgroundReducer(modified, {
                type: 'projectLoaded',
            });
            expect(state.projectModified).toBe(false);
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
