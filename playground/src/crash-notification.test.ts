import { expect, test } from 'vitest';

import { crashNotification } from './crash-notification';

test.each([
    [
        { kind: 'restarted' } as const,
        {
            message: 'The language server crashed and was restarted.',
            type: 'warning',
        },
    ],
    [
        { kind: 'waiting-for-edit' } as const,
        {
            message:
                'The language server crashed again on the same code. It will restart when you next edit a file.',
            type: 'error',
        },
    ],
])('crashNotification(%o)', (event, expected) => {
    expect(crashNotification(event)).toEqual(expected);
});
