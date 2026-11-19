import * as monaco from 'monaco-editor-core';

import type { ShownError } from './bug-report';

/**
 * Returns the errors the editor shows in the files at openPaths. Monaco keeps a model,
 * and its markers, for each file opened since the project loaded, so markers for other
 * paths belong to closed tabs.
 */
export function shownErrors(openPaths: readonly string[]): ShownError[] {
    const open = new Set(openPaths);
    return monaco.editor
        .getModelMarkers({})
        .filter(
            (marker) =>
                marker.severity === monaco.MarkerSeverity.Error &&
                open.has(marker.resource.path),
        )
        .map((marker) => ({
            path: marker.resource.path,
            line: marker.startLineNumber,
            column: marker.startColumn,
            message: marker.message,
        }));
}
