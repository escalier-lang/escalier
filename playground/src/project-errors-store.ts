import { create } from 'zustand';

import type { FSEventEmitter } from './fs/fs-events';
import {
    type ProjectErrors,
    type ProjectErrorsAction,
    forgetRemovedAction,
    projectErrorsReducer,
} from './project-errors';

type ProjectErrorsStore = {
    errors: ProjectErrors;
    dispatch: (action: ProjectErrorsAction) => void;
};

export const useProjectErrorsStore = create<ProjectErrorsStore>((set) => ({
    errors: {},
    dispatch: (action) =>
        set((state) => ({
            errors: projectErrorsReducer(state.errors, action),
        })),
}));

/**
 * Drops the errors of each file or directory that events reports deleted or renamed
 * away from the store.
 */
export function forgetRemovedFiles(events: Pick<FSEventEmitter, 'on'>): void {
    // The language server publishes diagnostics only for files that still exist, so the
    // errors of a removed file would stay in the count without this.
    events.on((event) => {
        const action = forgetRemovedAction(event);
        if (action !== null) {
            useProjectErrorsStore.getState().dispatch(action);
        }
    });
}
