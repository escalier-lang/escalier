import { create } from 'zustand';

import type { FSEventEmitter } from '../fs/fs-events';
import {
    type ProjectDiagnostics,
    type ProjectDiagnosticsAction,
    forgetRemovedAction,
    projectDiagnosticsReducer,
} from './project-diagnostics';

type ProjectDiagnosticsStore = {
    diagnostics: ProjectDiagnostics;
    dispatch: (action: ProjectDiagnosticsAction) => void;
};

export const useProjectDiagnosticsStore = create<ProjectDiagnosticsStore>(
    (set) => ({
        diagnostics: {},
        dispatch: (action) =>
            set((state) => ({
                diagnostics: projectDiagnosticsReducer(
                    state.diagnostics,
                    action,
                ),
            })),
    }),
);

/**
 * Drops the diagnostics of each file or directory that events reports deleted or
 * renamed away from the store.
 */
export function forgetRemovedFiles(events: Pick<FSEventEmitter, 'on'>): void {
    // The language server publishes diagnostics only for files that still exist, so the
    // diagnostics of a removed file would stay in the count without this.
    events.on((event) => {
        const action = forgetRemovedAction(event);
        if (action !== null) {
            useProjectDiagnosticsStore.getState().dispatch(action);
        }
    });
}
