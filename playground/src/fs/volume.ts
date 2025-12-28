import type { FSDir } from './fs-node.js';

export interface Volume {
    [path: string]: Uint8Array;
}

export function volumeToDir(volume: Volume): FSDir {
    const root: FSDir = { type: 'dir', name: '/', children: new Map() };

    for (const path in volume) {
        const parts = path.split('/').filter((part) => part.length > 0);
        let currentDir = root;

        for (let i = 0; i < parts.length; i++) {
            const part = parts[i];
            if (i === parts.length - 1) {
                // Last part, create file
                currentDir.children.set(part, {
                    type: 'file',
                    name: part,
                    content: volume[path],
                });
            } else {
                // Directory part
                if (!currentDir.children.has(part)) {
                    currentDir.children.set(part, {
                        type: 'dir',
                        name: part,
                        children: new Map(),
                    });
                }
                currentDir = currentDir.children.get(part) as FSDir;
            }
        }
    }

    return root;
}
