export type FSEventKind = 'file' | 'dir';
export type FSEventType = 'create' | 'delete' | 'rename';

export interface FSEvent {
    type: FSEventType;
    path: string;
    kind: FSEventKind;
    /** For rename events, the path before the rename. */
    oldPath?: string;
}

export type FSEventListener = (event: FSEvent) => void;

export class FSEventEmitter {
    private listeners: FSEventListener[] = [];

    on(listener: FSEventListener): void {
        this.listeners.push(listener);
    }

    off(listener: FSEventListener): void {
        this.listeners = this.listeners.filter((l) => l !== listener);
    }

    emit(event: FSEvent): void {
        for (const listener of this.listeners) {
            listener(event);
        }
    }
}
