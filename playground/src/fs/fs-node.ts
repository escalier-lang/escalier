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

export interface FSSymlink {
    type: 'symlink';
    name: string;
    target: string; // path as provided to symlink(), relative or absolute
}

export type FSNode = FSFile | FSDir | FSSymlink;
