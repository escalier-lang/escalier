import { describe, expect, test } from 'vitest';

import {
    type EditorState,
    editorReducer,
    initialEditorState,
} from './editor-state';

/** Helper to create state with specific open tabs. */
function stateWith(
    paths: string[],
    activeTabIndex: number | null = 0,
): EditorState {
    return {
        ...initialEditorState,
        leftTabs: paths.map((path) => ({ path })),
        activeLeftTabIndex: activeTabIndex,
    };
}

describe('editorReducer', () => {
    describe('openFile', () => {
        test('opens a new tab and activates it', () => {
            const state = stateWith(['/bin/main.esc']);
            const next = editorReducer(state, {
                type: 'openFile',
                path: '/lib/utils.esc',
            });
            expect(next.leftTabs).toHaveLength(2);
            expect(next.leftTabs[1].path).toBe('/lib/utils.esc');
            expect(next.activeLeftTabIndex).toBe(1);
        });

        test('activates existing tab instead of duplicating', () => {
            const state = stateWith(['/bin/main.esc', '/lib/utils.esc'], 0);
            const next = editorReducer(state, {
                type: 'openFile',
                path: '/lib/utils.esc',
            });
            expect(next.leftTabs).toHaveLength(2);
            expect(next.activeLeftTabIndex).toBe(1);
        });

        test('opens a tab when no tabs are open', () => {
            const state = stateWith([], null);
            const next = editorReducer(state, {
                type: 'openFile',
                path: '/bin/main.esc',
            });
            expect(next.leftTabs).toHaveLength(1);
            expect(next.activeLeftTabIndex).toBe(0);
        });

        test('opens on right when side is right', () => {
            const state = stateWith(['/a.esc']);
            const next = editorReducer(state, {
                type: 'openFile',
                path: '/build/bin/main.js',
                side: 'right',
            });
            expect(next.leftTabs).toHaveLength(1);
            expect(next.rightTabs).toHaveLength(1);
            expect(next.rightTabs[0].path).toBe('/build/bin/main.js');
            expect(next.activeRightTabIndex).toBe(0);
        });

        test('activates existing right tab instead of duplicating', () => {
            let state = editorReducer(initialEditorState, {
                type: 'openFile',
                path: '/build/bin/main.js',
                side: 'right',
            });
            state = editorReducer(state, {
                type: 'openFile',
                path: '/build/bin/main.js.map',
                side: 'right',
            });
            const next = editorReducer(state, {
                type: 'openFile',
                path: '/build/bin/main.js',
                side: 'right',
            });
            expect(next.rightTabs).toHaveLength(2);
            expect(next.activeRightTabIndex).toBe(0);
        });
    });

    describe('closeTab', () => {
        test('closes the only tab, activeTabIndex becomes null', () => {
            const state = stateWith(['/bin/main.esc'], 0);
            const next = editorReducer(state, {
                type: 'closeTab',
                side: 'left',
                index: 0,
            });
            expect(next.leftTabs).toHaveLength(0);
            expect(next.activeLeftTabIndex).toBeNull();
        });

        test('closing active tab activates the next tab', () => {
            const state = stateWith(['/a.esc', '/b.esc', '/c.esc'], 0);
            const next = editorReducer(state, {
                type: 'closeTab',
                side: 'left',
                index: 0,
            });
            expect(next.leftTabs).toHaveLength(2);
            expect(next.activeLeftTabIndex).toBe(0);
            expect(next.leftTabs[0].path).toBe('/b.esc');
        });

        test('closing last active tab activates the previous tab', () => {
            const state = stateWith(['/a.esc', '/b.esc', '/c.esc'], 2);
            const next = editorReducer(state, {
                type: 'closeTab',
                side: 'left',
                index: 2,
            });
            expect(next.leftTabs).toHaveLength(2);
            expect(next.activeLeftTabIndex).toBe(1);
        });

        test('closing a tab before the active tab shifts activeTabIndex left', () => {
            const state = stateWith(['/a.esc', '/b.esc', '/c.esc'], 2);
            const next = editorReducer(state, {
                type: 'closeTab',
                side: 'left',
                index: 0,
            });
            expect(next.leftTabs).toHaveLength(2);
            expect(next.activeLeftTabIndex).toBe(1);
            expect(next.leftTabs[1].path).toBe('/c.esc');
        });

        test('closing a tab after the active tab does not change activeTabIndex', () => {
            const state = stateWith(['/a.esc', '/b.esc', '/c.esc'], 0);
            const next = editorReducer(state, {
                type: 'closeTab',
                side: 'left',
                index: 2,
            });
            expect(next.leftTabs).toHaveLength(2);
            expect(next.activeLeftTabIndex).toBe(0);
        });

        test('ignores out-of-bounds index', () => {
            const state = stateWith(['/a.esc'], 0);
            expect(
                editorReducer(state, {
                    type: 'closeTab',
                    side: 'left',
                    index: 5,
                }),
            ).toBe(state);
            expect(
                editorReducer(state, {
                    type: 'closeTab',
                    side: 'left',
                    index: -1,
                }),
            ).toBe(state);
        });

        test('closes right tab', () => {
            const state = editorReducer(initialEditorState, {
                type: 'openFile',
                path: '/build/bin/main.js',
                side: 'right',
            });
            const next = editorReducer(state, {
                type: 'closeTab',
                side: 'right',
                index: 0,
            });
            expect(next.rightTabs).toHaveLength(0);
            expect(next.activeRightTabIndex).toBeNull();
        });
    });

    describe('setActiveTab', () => {
        test('sets the active tab index', () => {
            const state = stateWith(['/a.esc', '/b.esc'], 0);
            const next = editorReducer(state, {
                type: 'setActiveTab',
                side: 'left',
                index: 1,
            });
            expect(next.activeLeftTabIndex).toBe(1);
        });

        test('ignores out-of-bounds index', () => {
            const state = stateWith(['/a.esc'], 0);
            expect(
                editorReducer(state, {
                    type: 'setActiveTab',
                    side: 'left',
                    index: 5,
                }),
            ).toBe(state);
            expect(
                editorReducer(state, {
                    type: 'setActiveTab',
                    side: 'left',
                    index: -1,
                }),
            ).toBe(state);
        });

        test('sets the active right tab index', () => {
            let state = editorReducer(initialEditorState, {
                type: 'openFile',
                path: '/build/bin/main.js',
                side: 'right',
            });
            state = editorReducer(state, {
                type: 'openFile',
                path: '/build/bin/main.js.map',
                side: 'right',
            });
            const next = editorReducer(state, {
                type: 'setActiveTab',
                side: 'right',
                index: 0,
            });
            expect(next.activeRightTabIndex).toBe(0);
        });
    });

    describe('setFocusedSide', () => {
        test('sets focusedSide to right', () => {
            const next = editorReducer(initialEditorState, {
                type: 'setFocusedSide',
                side: 'right',
            });
            expect(next.focusedSide).toBe('right');
        });

        test('sets focusedSide to left', () => {
            const state: EditorState = {
                ...initialEditorState,
                focusedSide: 'right',
            };
            const next = editorReducer(state, {
                type: 'setFocusedSide',
                side: 'left',
            });
            expect(next.focusedSide).toBe('left');
        });
    });

    describe('openFile routing', () => {
        test('opens on left when focusedSide is left', () => {
            const state = stateWith(['/a.esc']);
            const next = editorReducer(state, {
                type: 'openFile',
                path: '/b.esc',
            });
            expect(next.leftTabs).toHaveLength(2);
            expect(next.leftTabs[1].path).toBe('/b.esc');
            expect(next.rightTabs).toHaveLength(0);
        });

        test('opens on right when focusedSide is right', () => {
            const state: EditorState = {
                ...stateWith(['/a.esc']),
                focusedSide: 'right',
            };
            const next = editorReducer(state, {
                type: 'openFile',
                path: '/b.esc',
            });
            expect(next.leftTabs).toHaveLength(1);
            expect(next.rightTabs).toHaveLength(1);
            expect(next.rightTabs[0].path).toBe('/b.esc');
        });
    });

    describe('moveTab', () => {
        test('moves tab from left to right', () => {
            const state = stateWith(['/a.esc', '/b.esc'], 0);
            const next = editorReducer(state, {
                type: 'moveTab',
                from: 'left',
                index: 0,
            });
            expect(next.leftTabs).toHaveLength(1);
            expect(next.leftTabs[0].path).toBe('/b.esc');
            expect(next.rightTabs).toHaveLength(1);
            expect(next.rightTabs[0].path).toBe('/a.esc');
            expect(next.activeRightTabIndex).toBe(0);
        });

        test('ignores out-of-bounds index when moving right', () => {
            const state = stateWith(['/a.esc'], 0);
            expect(
                editorReducer(state, {
                    type: 'moveTab',
                    from: 'left',
                    index: 5,
                }),
            ).toBe(state);
        });

        test('moves tab from right to left', () => {
            const state: EditorState = {
                ...stateWith(['/a.esc']),
                rightTabs: [{ path: '/build/bin/main.js' }],
                activeRightTabIndex: 0,
                focusedSide: 'right',
            };
            const next = editorReducer(state, {
                type: 'moveTab',
                from: 'right',
                index: 0,
            });
            expect(next.rightTabs).toHaveLength(0);
            expect(next.leftTabs).toHaveLength(2);
            expect(next.leftTabs[1].path).toBe('/build/bin/main.js');
            expect(next.activeLeftTabIndex).toBe(1);
        });

        test('works correctly even when focusedSide is right', () => {
            const state: EditorState = {
                ...initialEditorState,
                rightTabs: [
                    { path: '/build/bin/main.js' },
                    { path: '/build/bin/main.js.map' },
                ],
                activeRightTabIndex: 0,
                focusedSide: 'right',
            };
            const next = editorReducer(state, {
                type: 'moveTab',
                from: 'right',
                index: 0,
            });
            expect(next.leftTabs).toHaveLength(2);
            expect(next.leftTabs[1].path).toBe('/build/bin/main.js');
            expect(next.rightTabs).toHaveLength(1);
            expect(next.rightTabs[0].path).toBe('/build/bin/main.js.map');
        });

        test('ignores out-of-bounds index when moving left', () => {
            const state = stateWith(['/a.esc'], 0);
            expect(
                editorReducer(state, {
                    type: 'moveTab',
                    from: 'right',
                    index: 5,
                }),
            ).toBe(state);
        });
    });

    describe('renameFile', () => {
        test('renames a matching tab', () => {
            const state = stateWith(['/a.esc', '/b.esc'], 0);
            const next = editorReducer(state, {
                type: 'renameFile',
                oldPath: '/a.esc',
                newPath: '/renamed.esc',
            });
            expect(next.leftTabs[0].path).toBe('/renamed.esc');
            expect(next.leftTabs[1].path).toBe('/b.esc');
        });

        test('does not modify tabs when path not found', () => {
            const state = stateWith(['/a.esc'], 0);
            const next = editorReducer(state, {
                type: 'renameFile',
                oldPath: '/nonexistent.esc',
                newPath: '/renamed.esc',
            });
            expect(next.leftTabs[0].path).toBe('/a.esc');
        });
    });

    describe('deleteFile', () => {
        test('closes the tab for the deleted file', () => {
            const state = stateWith(['/a.esc', '/b.esc'], 0);
            const next = editorReducer(state, {
                type: 'deleteFile',
                path: '/a.esc',
            });
            expect(next.leftTabs).toHaveLength(1);
            expect(next.leftTabs[0].path).toBe('/b.esc');
        });

        test('no-ops if the file is not open', () => {
            const state = stateWith(['/a.esc'], 0);
            const next = editorReducer(state, {
                type: 'deleteFile',
                path: '/nonexistent.esc',
            });
            expect(next).toBe(state);
        });
    });

    describe('resetTabs', () => {
        test('resets to default primary file', () => {
            const state = stateWith(['/a.esc', '/b.esc', '/c.esc'], 2);
            const next = editorReducer(state, { type: 'resetTabs' });
            expect(next.leftTabs).toEqual([{ path: '/lib/index.esc' }]);
            expect(next.activeLeftTabIndex).toBe(0);
            expect(next.rightTabs).toEqual([]);
            expect(next.activeRightTabIndex).toBeNull();
        });

        test('resets to a custom primary file', () => {
            const next = editorReducer(initialEditorState, {
                type: 'resetTabs',
                primaryFile: '/bin/app.esc',
            });
            expect(next.leftTabs).toEqual([{ path: '/bin/app.esc' }]);
            expect(next.activeLeftTabIndex).toBe(0);
        });
    });

    describe('showNotification', () => {
        test('sets the notification', () => {
            const notification = { message: 'hello', type: 'info' as const };
            const next = editorReducer(initialEditorState, {
                type: 'showNotification',
                notification,
            });
            expect(next.notification).toBe(notification);
        });
    });

    describe('dismissNotification', () => {
        test('clears the notification', () => {
            const state: EditorState = {
                ...initialEditorState,
                notification: { message: 'hello', type: 'info' },
            };
            const next = editorReducer(state, {
                type: 'dismissNotification',
            });
            expect(next.notification).toBeNull();
        });
    });
});
