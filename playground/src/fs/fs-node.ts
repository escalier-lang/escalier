export interface FSFile {
    type: 'file';
    name: string;
    content: Uint8Array;
}

export interface FSDir {
    type: 'dir';
    name: string;
    children: Map<string, FSNode>;
}

export type FSNode = FSFile | FSDir;
