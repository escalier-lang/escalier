import { beforeEach, expect, test } from 'vitest';

import { FSEventEmitter } from './fs/fs-events';
import type { ProjectDiagnostic } from './project-diagnostics';
import {
    forgetRemovedFiles,
    useProjectDiagnosticsStore,
} from './project-diagnostics-store';

const diagnostic: ProjectDiagnostic = {
    path: '/lib/a.esc',
    line: 1,
    column: 1,
    severity: 'error',
    message: 'bad',
};

beforeEach(() => {
    useProjectDiagnosticsStore.setState({ diagnostics: {} });
});

test('forgetRemovedFiles drops the diagnostics of a deleted file', () => {
    const events = new FSEventEmitter();
    forgetRemovedFiles(events);
    useProjectDiagnosticsStore.getState().dispatch({
        type: 'setFileDiagnostics',
        path: '/lib/a.esc',
        diagnostics: [diagnostic],
    });

    events.emit({ type: 'change', kind: 'file', path: '/lib/a.esc' });
    expect(useProjectDiagnosticsStore.getState().diagnostics).toEqual({
        '/lib/a.esc': [diagnostic],
    });

    events.emit({ type: 'delete', kind: 'file', path: '/lib/a.esc' });
    expect(useProjectDiagnosticsStore.getState().diagnostics).toEqual({});
});
