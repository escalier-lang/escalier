import { describe, expect, test } from 'vitest';

import { type PlaygroundState, initialState, playgroundReducer } from './state';

/** Helper to create state with specific open tabs. */
function stateWith(
    paths: string[],
    activeTabIndex: number | null = 0,
): PlaygroundState {
    return {
        ...initialState,
        openTabs: paths.map((path) => ({ path })),
        activeTabIndex,
    };
}

describe('playgroundReducer', () => {
    describe('openFile', () => {
        test('opens a new tab and activates it', () => {
            const state = stateWith(['/bin/main.esc']);
            const next = playgroundReducer(state, {
                type: 'openFile',
                path: '/lib/utils.esc',
            });
            expect(next.openTabs).toHaveLength(2);
            expect(next.openTabs[1].path).toBe('/lib/utils.esc');
            expect(next.activeTabIndex).toBe(1);
        });

        test('activates existing tab instead of duplicating', () => {
            const state = stateWith(['/bin/main.esc', '/lib/utils.esc'], 0);
            const next = playgroundReducer(state, {
                type: 'openFile',
                path: '/lib/utils.esc',
            });
            expect(next.openTabs).toHaveLength(2);
            expect(next.activeTabIndex).toBe(1);
        });

        test('opens a tab when no tabs are open', () => {
            const state = stateWith([], null);
            const next = playgroundReducer(state, {
                type: 'openFile',
                path: '/bin/main.esc',
            });
            expect(next.openTabs).toHaveLength(1);
            expect(next.activeTabIndex).toBe(0);
        });
    });

    describe('closeTab', () => {
        test('closes the only tab, activeTabIndex becomes null', () => {
            const state = stateWith(['/bin/main.esc'], 0);
            const next = playgroundReducer(state, {
                type: 'closeTab',
                index: 0,
            });
            expect(next.openTabs).toHaveLength(0);
            expect(next.activeTabIndex).toBeNull();
        });

        test('closing active tab activates the next tab', () => {
            const state = stateWith(['/a.esc', '/b.esc', '/c.esc'], 0);
            const next = playgroundReducer(state, {
                type: 'closeTab',
                index: 0,
            });
            expect(next.openTabs).toHaveLength(2);
            expect(next.activeTabIndex).toBe(0);
            expect(next.openTabs[0].path).toBe('/b.esc');
        });

        test('closing last active tab activates the previous tab', () => {
            const state = stateWith(['/a.esc', '/b.esc', '/c.esc'], 2);
            const next = playgroundReducer(state, {
                type: 'closeTab',
                index: 2,
            });
            expect(next.openTabs).toHaveLength(2);
            expect(next.activeTabIndex).toBe(1);
        });

        test('closing a tab before the active tab shifts activeTabIndex left', () => {
            const state = stateWith(['/a.esc', '/b.esc', '/c.esc'], 2);
            const next = playgroundReducer(state, {
                type: 'closeTab',
                index: 0,
            });
            expect(next.openTabs).toHaveLength(2);
            expect(next.activeTabIndex).toBe(1);
            expect(next.openTabs[1].path).toBe('/c.esc');
        });

        test('closing a tab after the active tab does not change activeTabIndex', () => {
            const state = stateWith(['/a.esc', '/b.esc', '/c.esc'], 0);
            const next = playgroundReducer(state, {
                type: 'closeTab',
                index: 2,
            });
            expect(next.openTabs).toHaveLength(2);
            expect(next.activeTabIndex).toBe(0);
        });

        test('ignores out-of-bounds index', () => {
            const state = stateWith(['/a.esc'], 0);
            expect(
                playgroundReducer(state, { type: 'closeTab', index: 5 }),
            ).toBe(state);
            expect(
                playgroundReducer(state, { type: 'closeTab', index: -1 }),
            ).toBe(state);
        });
    });

    describe('setActiveTab', () => {
        test('sets the active tab index', () => {
            const state = stateWith(['/a.esc', '/b.esc'], 0);
            const next = playgroundReducer(state, {
                type: 'setActiveTab',
                index: 1,
            });
            expect(next.activeTabIndex).toBe(1);
        });

        test('ignores out-of-bounds index', () => {
            const state = stateWith(['/a.esc'], 0);
            expect(
                playgroundReducer(state, { type: 'setActiveTab', index: 5 }),
            ).toBe(state);
            expect(
                playgroundReducer(state, { type: 'setActiveTab', index: -1 }),
            ).toBe(state);
        });
    });

    describe('openRightFile', () => {
        test('opens a new right tab and activates it', () => {
            const next = playgroundReducer(initialState, {
                type: 'openRightFile',
                path: '/build/bin/main.js',
            });
            expect(next.rightTabs).toHaveLength(1);
            expect(next.rightTabs[0].path).toBe('/build/bin/main.js');
            expect(next.activeRightTabIndex).toBe(0);
        });

        test('activates existing right tab instead of duplicating', () => {
            let state = playgroundReducer(initialState, {
                type: 'openRightFile',
                path: '/build/bin/main.js',
            });
            state = playgroundReducer(state, {
                type: 'openRightFile',
                path: '/build/bin/main.js.map',
            });
            const next = playgroundReducer(state, {
                type: 'openRightFile',
                path: '/build/bin/main.js',
            });
            expect(next.rightTabs).toHaveLength(2);
            expect(next.activeRightTabIndex).toBe(0);
        });
    });

    describe('closeRightTab', () => {
        test('closes the only right tab', () => {
            const state = playgroundReducer(initialState, {
                type: 'openRightFile',
                path: '/build/bin/main.js',
            });
            const next = playgroundReducer(state, {
                type: 'closeRightTab',
                index: 0,
            });
            expect(next.rightTabs).toHaveLength(0);
            expect(next.activeRightTabIndex).toBeNull();
        });
    });

    describe('setActiveRightTab', () => {
        test('sets the active right tab index', () => {
            let state = playgroundReducer(initialState, {
                type: 'openRightFile',
                path: '/build/bin/main.js',
            });
            state = playgroundReducer(state, {
                type: 'openRightFile',
                path: '/build/bin/main.js.map',
            });
            const next = playgroundReducer(state, {
                type: 'setActiveRightTab',
                index: 0,
            });
            expect(next.activeRightTabIndex).toBe(0);
        });
    });

    describe('setInitialCompileDone', () => {
        test('sets initialCompileDone to true', () => {
            const next = playgroundReducer(initialState, {
                type: 'setInitialCompileDone',
            });
            expect(next.initialCompileDone).toBe(true);
        });
    });

    describe('renameFile', () => {
        test('renames a matching tab', () => {
            const state = stateWith(['/a.esc', '/b.esc'], 0);
            const next = playgroundReducer(state, {
                type: 'renameFile',
                oldPath: '/a.esc',
                newPath: '/renamed.esc',
            });
            expect(next.openTabs[0].path).toBe('/renamed.esc');
            expect(next.openTabs[1].path).toBe('/b.esc');
        });

        test('does not modify tabs when path not found', () => {
            const state = stateWith(['/a.esc'], 0);
            const next = playgroundReducer(state, {
                type: 'renameFile',
                oldPath: '/nonexistent.esc',
                newPath: '/renamed.esc',
            });
            expect(next.openTabs[0].path).toBe('/a.esc');
        });
    });

    describe('deleteFile', () => {
        test('closes the tab for the deleted file', () => {
            const state = stateWith(['/a.esc', '/b.esc'], 0);
            const next = playgroundReducer(state, {
                type: 'deleteFile',
                path: '/a.esc',
            });
            expect(next.openTabs).toHaveLength(1);
            expect(next.openTabs[0].path).toBe('/b.esc');
        });

        test('no-ops if the file is not open', () => {
            const state = stateWith(['/a.esc'], 0);
            const next = playgroundReducer(state, {
                type: 'deleteFile',
                path: '/nonexistent.esc',
            });
            expect(next).toBe(state);
        });
    });

    describe('resetTabs', () => {
        test('resets to default primary file', () => {
            const state = stateWith(['/a.esc', '/b.esc', '/c.esc'], 2);
            const next = playgroundReducer(state, { type: 'resetTabs' });
            expect(next.openTabs).toEqual([{ path: '/lib/index.esc' }]);
            expect(next.activeTabIndex).toBe(0);
            expect(next.rightTabs).toEqual([]);
            expect(next.activeRightTabIndex).toBeNull();
            expect(next.initialCompileDone).toBe(false);
        });

        test('resets to a custom primary file', () => {
            const next = playgroundReducer(initialState, {
                type: 'resetTabs',
                primaryFile: '/bin/app.esc',
            });
            expect(next.openTabs).toEqual([{ path: '/bin/app.esc' }]);
            expect(next.activeTabIndex).toBe(0);
        });
    });

    describe('setValidationResult', () => {
        test('sets the validation result', () => {
            const result = { mode: 'invalid' as const, errors: ['bad'] };
            const next = playgroundReducer(initialState, {
                type: 'setValidationResult',
                result,
            });
            expect(next.validationResult).toBe(result);
        });
    });

    describe('showNotification', () => {
        test('sets the notification', () => {
            const notification = { message: 'hello', type: 'info' as const };
            const next = playgroundReducer(initialState, {
                type: 'showNotification',
                notification,
            });
            expect(next.notification).toBe(notification);
        });
    });

    describe('dismissNotification', () => {
        test('clears the notification', () => {
            const state: PlaygroundState = {
                ...initialState,
                notification: { message: 'hello', type: 'info' },
            };
            const next = playgroundReducer(state, {
                type: 'dismissNotification',
            });
            expect(next.notification).toBeNull();
        });
    });
});
