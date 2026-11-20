import { beforeEach, expect, test } from 'vitest';

import { FSEventEmitter } from './fs/fs-events';
import {
    forgetRemovedFiles,
    useProjectErrorsStore,
} from './project-errors-store';

const error = { path: '/lib/a.esc', line: 1, column: 1, message: 'bad' };

beforeEach(() => {
    useProjectErrorsStore.setState({ errors: {} });
});

test('forgetRemovedFiles drops the errors of a deleted file', () => {
    const events = new FSEventEmitter();
    forgetRemovedFiles(events);
    useProjectErrorsStore.getState().dispatch({
        type: 'setFileErrors',
        path: '/lib/a.esc',
        errors: [error],
    });

    events.emit({ type: 'change', kind: 'file', path: '/lib/a.esc' });
    expect(useProjectErrorsStore.getState().errors).toEqual({
        '/lib/a.esc': [error],
    });

    events.emit({ type: 'delete', kind: 'file', path: '/lib/a.esc' });
    expect(useProjectErrorsStore.getState().errors).toEqual({});
});
