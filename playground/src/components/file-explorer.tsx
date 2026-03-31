import { useEffect, useState } from 'react';

import type { BrowserFS } from '../fs/browser-fs';
import type { FSDir, FSNode } from '../fs/fs-node';
import styles from './file-explorer.module.css';

type FileExplorerProps = {
    fs: BrowserFS;
    onFileOpen: (path: string) => void;
};

export const FileExplorer = ({ fs, onFileOpen }: FileExplorerProps) => {
    const [, setRev] = useState(0);

    // Re-render when FS changes
    useEffect(() => {
        const listener = () => setRev((r) => r + 1);
        fs.events.on(listener);
        return () => fs.events.off(listener);
    }, [fs]);

    return (
        <div className={styles.explorer}>
            <div className={styles.header}>EXPLORER</div>
            <div className={styles.tree}>
                <DirChildren
                    dir={fs.rootDir}
                    parentPath=""
                    onFileClick={onFileOpen}
                />
            </div>
        </div>
    );
};

/** Directories/files to hide from the explorer */
function isHidden(name: string): boolean {
    return name === '.pnpm';
}

type DirChildrenProps = {
    dir: FSDir;
    parentPath: string;
    onFileClick: (path: string) => void;
};

const DirChildren = ({ dir, parentPath, onFileClick }: DirChildrenProps) => {
    // Sort: directories first, then files, alphabetical within each group
    const entries = Array.from(dir.children.entries())
        .filter(([name]) => !isHidden(name))
        .sort(([aName, aNode], [bName, bNode]) => {
            const aIsDir = aNode.type === 'dir' ? 0 : 1;
            const bIsDir = bNode.type === 'dir' ? 0 : 1;
            if (aIsDir !== bIsDir) return aIsDir - bIsDir;
            return aName.localeCompare(bName);
        });

    return (
        <ul className={styles.list}>
            {entries.map(([name, node]) => {
                const path = `${parentPath}/${name}`;
                return (
                    <TreeNode
                        key={name}
                        name={name}
                        node={node}
                        path={path}
                        onFileClick={onFileClick}
                    />
                );
            })}
        </ul>
    );
};

type TreeNodeProps = {
    name: string;
    node: FSNode;
    path: string;
    onFileClick: (path: string) => void;
};

const TreeNode = ({ name, node, path, onFileClick }: TreeNodeProps) => {
    const startCollapsed = name === 'node_modules' || name === 'build';
    const [expanded, setExpanded] = useState(!startCollapsed);
    const isDimmed =
        path.startsWith('/build') ||
        path.startsWith('/node_modules') ||
        /^\/packages\/[^/]+\/build/.test(path);

    if (node.type === 'dir') {
        return (
            <li>
                <button
                    type="button"
                    className={`${styles.entry} ${styles.dirEntry} ${isDimmed ? styles.dimmed : ''}`}
                    onClick={() => setExpanded((e) => !e)}
                    aria-expanded={expanded}
                >
                    <span className={styles.chevron}>
                        {expanded ? '\u25BE' : '\u25B8'}
                    </span>
                    {name}
                </button>
                {expanded && (
                    <DirChildren
                        dir={node}
                        parentPath={path}
                        onFileClick={onFileClick}
                    />
                )}
            </li>
        );
    }

    if (node.type === 'file') {
        return (
            <li>
                <button
                    type="button"
                    className={`${styles.entry} ${styles.fileEntry} ${isDimmed ? styles.dimmed : ''}`}
                    onClick={() => onFileClick(path)}
                >
                    {name}
                </button>
            </li>
        );
    }

    // symlinks - show but don't make interactive
    return (
        <li>
            <div className={`${styles.entry} ${styles.dimmed}`}>
                {name} &rarr;
            </div>
        </li>
    );
};
