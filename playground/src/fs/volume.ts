import type { FSDir } from './fs-node.js';

export interface VolumeEntry {
    url?: string;
    content: Uint8Array | null;
}

export interface Volume {
    [path: string]: VolumeEntry;
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
                    content: volume[path].content ?? new Uint8Array(0),
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

const libCode = `export fn add(a: number, b: number) -> number {
    return a + b
}

export fn subtract(a: number, b: number) -> number {
    return a - b
}
`;

const binCode = `val sum = add(5, 10)
val diff = subtract(10, 3)
console.log("sum =", sum)
console.log("diff =", diff)
`;

export function createVolume(manifest: string[], baseUrl: string): Volume {
    const vol: Volume = {
        '/package.json': {
            content: new TextEncoder().encode(
                JSON.stringify({
                    name: 'my-project',
                    version: '1.0.0',
                    main: 'build/bin/main.js',
                }),
            ),
        },
        '/escalier.toml': {
            content: new TextEncoder().encode(
                `[project]
name = "my-project"
`,
            ),
        },
        '/lib/math.esc': {
            content: new TextEncoder().encode(libCode),
        },
        '/bin/main.esc': {
            content: new TextEncoder().encode(binCode),
        },
    };

    for (const filename of manifest) {
        vol[`/node_modules/typescript/lib/${filename}`] = {
            url: `${baseUrl}types/${filename}`,
            content: null,
        };
    }

    return vol;
}
