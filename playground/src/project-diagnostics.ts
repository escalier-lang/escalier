import type { ShownError } from './bug-report';
import type { FSEvent } from './fs/fs-events';

/** How serious a diagnostic is, as the language server rates it. */
export type DiagnosticSeverity = 'error' | 'warning' | 'info' | 'hint';

/** A diagnostic the language server reported for a project file. */
export type ProjectDiagnostic = ShownError & { severity: DiagnosticSeverity };

/** The diagnostics the language server last reported for each project file, keyed by path. */
export type ProjectDiagnostics = Readonly<
    Record<string, readonly ProjectDiagnostic[]>
>;

export type ProjectDiagnosticsAction =
    /** Replaces the diagnostics of the file at path. */
    | {
          type: 'setFileDiagnostics';
          path: string;
          diagnostics: readonly ProjectDiagnostic[];
      }
    /** Drops the diagnostics of the file at path, or of every file under the directory at path. */
    | { type: 'forgetPath'; path: string }
    /** Drops the diagnostics of every file. */
    | { type: 'forgetAll' };

/**
 * The path of the tab that lists every diagnostic in the project. No file has this
 * path, so the editor fills the tab from the project's diagnostics.
 */
export const DIAGNOSTICS_TAB_PATH = '/.playground/Diagnostics';

export function projectDiagnosticsReducer(
    state: ProjectDiagnostics,
    action: ProjectDiagnosticsAction,
): ProjectDiagnostics {
    switch (action.type) {
        case 'setFileDiagnostics': {
            const { [action.path]: _previous, ...rest } = state;
            return action.diagnostics.length === 0
                ? rest
                : { ...rest, [action.path]: action.diagnostics };
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

/** How many errors and warnings a project has across all its files. */
export type DiagnosticCounts = { errors: number; warnings: number };

/** Returns how many errors and warnings diagnostics holds across all files. */
export function diagnosticCounts(
    diagnostics: ProjectDiagnostics,
): DiagnosticCounts {
    const counts = { errors: 0, warnings: 0 };
    for (const diagnostic of Object.values(diagnostics).flat()) {
        if (diagnostic.severity === 'error') counts.errors++;
        if (diagnostic.severity === 'warning') counts.warnings++;
    }
    return counts;
}

/** Returns count followed by noun, made plural unless count is 1, such as "2 errors". */
export function countOf(count: number, noun: string): string {
    return `${count} ${noun}${count === 1 ? '' : 's'}`;
}

/**
 * Returns the text of the diagnostics tab: one `path:line:column: severity: message`
 * line per diagnostic, sorted by path, line and column, or "No diagnostics." when there
 * are none.
 */
export function projectDiagnosticsText(
    diagnostics: ProjectDiagnostics,
): string {
    const all = Object.values(diagnostics)
        .flat()
        .sort(
            (a, b) =>
                a.path.localeCompare(b.path) ||
                a.line - b.line ||
                a.column - b.column,
        );
    if (all.length === 0) return 'No diagnostics.\n';
    return all
        .map(
            (d) =>
                `${d.path}:${d.line}:${d.column}: ${d.severity}: ${d.message}\n`,
        )
        .join('');
}

/**
 * Returns the action that drops the diagnostics of what event removed, or null when
 * event removes nothing. A delete removes its path and a rename removes its old path.
 */
export function forgetRemovedAction(
    event: FSEvent,
): ProjectDiagnosticsAction | null {
    if (event.type === 'delete') {
        return { type: 'forgetPath', path: event.path };
    }
    if (event.type === 'rename' && event.oldPath !== undefined) {
        return { type: 'forgetPath', path: event.oldPath };
    }
    return null;
}
