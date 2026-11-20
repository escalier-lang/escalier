import { type ShownError, errorsText } from './bug-report';
import type { FSEvent } from './fs/fs-events';

/** The errors the language server last reported for each project file, keyed by path. */
export type ProjectErrors = Readonly<Record<string, readonly ShownError[]>>;

export type ProjectErrorsAction =
    /** Replaces the errors of the file at path. */
    | { type: 'setFileErrors'; path: string; errors: readonly ShownError[] }
    /** Drops the errors of the file at path, or of every file under the directory at path. */
    | { type: 'forgetPath'; path: string }
    /** Drops the errors of every file. */
    | { type: 'forgetAll' };

/**
 * The path of the tab that lists every error in the project. No file has this path, so
 * the editor fills the tab from the project's errors.
 */
export const ERRORS_TAB_PATH = '/.playground/Errors';

export function projectErrorsReducer(
    state: ProjectErrors,
    action: ProjectErrorsAction,
): ProjectErrors {
    switch (action.type) {
        case 'setFileErrors': {
            const { [action.path]: _previous, ...rest } = state;
            return action.errors.length === 0
                ? rest
                : { ...rest, [action.path]: action.errors };
        }
        case 'forgetPath': {
            const under = `${action.path}/`;
            return Object.fromEntries(
                Object.entries(state).filter(
                    ([path]) => path !== action.path && !path.startsWith(under),
                ),
            );
        }
        case 'forgetAll':
            return {};
    }
}

/** Returns how many errors errors holds across all files. */
export function errorCount(errors: ProjectErrors): number {
    return Object.values(errors).reduce((sum, list) => sum + list.length, 0);
}

/** Returns count as the status bar shows it, such as "No errors" or "3 errors". */
export function errorCountLabel(count: number): string {
    if (count === 0) return 'No errors';
    return count === 1 ? '1 error' : `${count} errors`;
}

/**
 * Returns the text of the errors tab: one `path:line:column: message` line per error,
 * sorted as errorsText sorts them, or "No errors." when there are none.
 */
export function projectErrorsText(errors: ProjectErrors): string {
    const all = Object.values(errors).flat();
    return all.length === 0 ? 'No errors.\n' : `${errorsText(all)}\n`;
}

/**
 * Returns the action that drops the errors of what event removed, or null when event
 * removes nothing. A delete removes its path and a rename removes its old path.
 */
export function forgetRemovedAction(
    event: FSEvent,
): ProjectErrorsAction | null {
    if (event.type === 'delete') {
        return { type: 'forgetPath', path: event.path };
    }
    if (event.type === 'rename' && event.oldPath !== undefined) {
        return { type: 'forgetPath', path: event.oldPath };
    }
    return null;
}
