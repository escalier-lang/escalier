import type { Notification } from './editor-state';
import type { ServerCrashEvent } from './lsp-client/client';

/** Returns the toast that tells the user the language server crashed. */
export function crashNotification(event: ServerCrashEvent): Notification {
    switch (event.kind) {
        case 'restarted':
            return {
                message: 'The language server crashed and was restarted.',
                type: 'warning',
            };
        case 'waiting-for-edit':
            return {
                message:
                    'The language server crashed again on the same code. It will restart when you next edit a file.',
                type: 'error',
            };
    }
}
